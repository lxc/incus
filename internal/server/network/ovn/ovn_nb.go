package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-org/libovsdb/client"

	"github.com/lxc/incus/internal/linux"
	ovnNB "github.com/lxc/incus/internal/server/network/ovn/schema/ovn-nb"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/subprocess"
)

// NB client.
type NB struct {
	client ovsdbClient.Client
	cookie ovsdbClient.MonitorCookie

	// For nbctl command calls.
	dbAddr        string
	sslCACert     string
	sslClientCert string
	sslClientKey  string
}

// NewNB initialises new OVN client for Northbound operations.
func NewNB(s *state.State) (*NB, error) {
	// Get database connection string.
	dbAddr := s.GlobalConfig.NetworkOVNNorthboundConnection()

	// Create the NB struct.
	client := &NB{
		dbAddr: dbAddr,
	}

	// Prepare the OVSDB client.
	dbSchema, err := ovnNB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard)}
	for _, entry := range strings.Split(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// Handle SSL.
	if strings.Contains(dbAddr, "ssl:") {
		// Get the OVN SSL keys from the daemon config.
		sslCACert, sslClientCert, sslClientKey := s.GlobalConfig.NetworkOVNSSL()

		// Fallback to filesystem keys.
		if sslCACert == "" {
			content, err := os.ReadFile("/etc/ovn/ovn-central.crt")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL CA certificate defined")
				}

				return nil, err
			}

			sslCACert = string(content)
		}

		if sslClientCert == "" {
			content, err := os.ReadFile("/etc/ovn/cert_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client certificate defined")
				}

				return nil, err
			}

			sslClientCert = string(content)
		}

		if sslClientKey == "" {
			content, err := os.ReadFile("/etc/ovn/key_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client key defined")
				}

				return nil, err
			}

			sslClientKey = string(content)
		}

		// Validation.
		if sslClientCert == "" {
			return nil, fmt.Errorf("OVN is configured to use SSL but no client certificate was found")
		}

		if sslClientKey == "" {
			return nil, fmt.Errorf("OVN is configured to use SSL but no client key was found")
		}

		// Prepare the client.
		clientCert, err := tls.X509KeyPair([]byte(sslClientCert), []byte(sslClientKey))
		if err != nil {
			return nil, err
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{clientCert},
			InsecureSkipVerify: true,
		}

		// Add CA check if provided.
		if sslCACert != "" {
			tlsCAder, _ := pem.Decode([]byte(sslCACert))
			if tlsCAder == nil {
				return nil, fmt.Errorf("Couldn't parse CA certificate")
			}

			tlsCAcert, err := x509.ParseCertificate(tlsCAder.Bytes)
			if err != nil {
				return nil, err
			}

			tlsCAcert.IsCA = true
			tlsCAcert.KeyUsage = x509.KeyUsageCertSign

			clientCAPool := x509.NewCertPool()
			clientCAPool.AddCert(tlsCAcert)

			tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
				if len(rawCerts) < 1 {
					return fmt.Errorf("Missing server certificate")
				}

				// Load the chain.
				roots := x509.NewCertPool()
				for _, rawCert := range rawCerts {
					cert, _ := x509.ParseCertificate(rawCert)
					if cert != nil {
						roots.AddCert(cert)
					}
				}

				// Load the main server certificate.
				cert, _ := x509.ParseCertificate(rawCerts[0])
				if cert == nil {
					return fmt.Errorf("Bad server certificate")
				}

				// Validate.
				opts := x509.VerifyOptions{
					Roots: roots,
				}

				_, err := cert.Verify(opts)
				return err
			}
		}

		// Add the TLS config to the client.
		options = append(options, ovsdbClient.WithTLSConfig(tlsConfig))

		// Fill the fields need for the CLI calls.
		client.sslCACert = sslCACert
		client.sslClientCert = sslClientCert
		client.sslClientKey = sslClientKey
	}

	// Connect to OVSDB.
	ovn, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, err
	}

	err = ovn.Connect(context.TODO())
	if err != nil {
		return nil, err
	}

	err = ovn.Echo(context.TODO())
	if err != nil {
		return nil, err
	}

	monitorCookie, err := ovn.MonitorAll(context.TODO())
	if err != nil {
		return nil, err
	}

	// Add the client to the struct.
	client.client = ovn
	client.cookie = monitorCookie

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *NB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
	})

	return client, nil
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *NB) nbctl(extraArgs ...string) (string, error) {
	// Figure out args.
	args := []string{"--timeout=10", "--db", o.dbAddr}

	// Handle SSL args.
	files := []*os.File{}
	if strings.Contains(o.dbAddr, "ssl:") {
		// Handle client certificate.
		clientCertFile, err := linux.CreateMemfd([]byte(o.sslClientCert))
		if err != nil {
			return "", err
		}

		defer clientCertFile.Close()
		files = append(files, clientCertFile)

		// Handle client key.
		clientKeyFile, err := linux.CreateMemfd([]byte(o.sslClientKey))
		if err != nil {
			return "", err
		}

		defer clientKeyFile.Close()
		files = append(files, clientKeyFile)

		// Handle CA certificate.
		caCertFile, err := linux.CreateMemfd([]byte(o.sslCACert))
		if err != nil {
			return "", err
		}

		defer caCertFile.Close()
		files = append(files, caCertFile)

		args = append(args,
			"-c", "/proc/self/fd/3",
			"-p", "/proc/self/fd/4",
			"-C", "/proc/self/fd/5",
		)
	}

	args = append(args, extraArgs...)
	return subprocess.RunCommandInheritFds(context.Background(), files, "ovn-nbctl", args...)
}
