package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-org/libovsdb/client"
	ovsdbModel "github.com/ovn-org/libovsdb/model"

	"github.com/lxc/incus/internal/linux"
	ovnNB "github.com/lxc/incus/internal/server/network/ovn/schema/ovn-nb"
	ovnSB "github.com/lxc/incus/internal/server/network/ovn/schema/ovn-sb"
	"github.com/lxc/incus/internal/server/network/ovs"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/subprocess"
)

// NewOVN initialises new OVN client wrapper with the connection set in network.ovn.northbound_connection config.
func NewOVN(s *state.State) (*OVN, error) {
	// Get database connection strings.
	nbConnection := s.GlobalConfig.NetworkOVNNorthboundConnection()
	sbConnection, err := ovs.NewOVS().OVNSouthboundDBRemoteAddress()
	if err != nil {
		return nil, fmt.Errorf("Failed to get OVN southbound connection string: %w", err)
	}

	// Create the OVN struct.
	client := &OVN{
		nbDBAddr: nbConnection,
		sbDBAddr: sbConnection,
	}

	// If using SSL, then get the CA and client key pair.
	if strings.Contains(nbConnection, "ssl:") {
		sslCACert, sslClientCert, sslClientKey := s.GlobalConfig.NetworkOVNSSL()

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

		client.sslCACert = sslCACert
		client.sslClientCert = sslClientCert
		client.sslClientKey = sslClientKey
	}

	return client, nil
}

// OVN command wrapper.
type OVN struct {
	mu sync.Mutex

	nbDBAddr string
	sbDBAddr string

	sslCACert     string
	sslClientCert string
	sslClientKey  string

	ovsdbClient map[ovnDatabase]*ovnClient
}

type ovnClient struct {
	client ovsdbClient.Client
	group  sync.WaitGroup
}

// ovnDatabase represents the OVN database to connect to.
type ovnDatabase string

const ovnDatabaseNorthbound = ovnDatabase("nortbound")
const ovnDatabaseSouthbound = ovnDatabase("southbound")

func (o *OVN) client(database ovnDatabase) (ovsdbClient.Client, func(), error) {
	// Check if we already have a client.
	o.mu.Lock()
	defer o.mu.Unlock()
	client, ok := o.ovsdbClient[database]
	if ok {
		client.group.Add(1)
		return client.client, func() { client.group.Done() }, nil
	}

	// Figure out the database address and schema.
	var dbAddr string
	var dbSchema ovsdbModel.ClientDBModel

	if database == ovnDatabaseNorthbound {
		var err error
		dbAddr = o.getNorthboundDB()

		dbSchema, err = ovnNB.FullDatabaseModel()
		if err != nil {
			return nil, nil, err
		}
	} else if database == ovnDatabaseSouthbound {
		var err error
		dbAddr = o.getSouthboundDB()

		dbSchema, err = ovnSB.FullDatabaseModel()
		if err != nil {
			return nil, nil, err
		}
	} else {
		return nil, nil, fmt.Errorf("Unsupported database type %q", database)
	}

	// Prepare the client.
	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard)}
	for _, entry := range strings.Split(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// Handle SSL.
	if strings.Contains(dbAddr, "ssl:") {
		clientCert, err := tls.X509KeyPair([]byte(o.sslClientCert), []byte(o.sslClientKey))
		if err != nil {
			return nil, nil, err
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{clientCert},
			InsecureSkipVerify: true,
		}

		// If provided with a CA certificate, setup a validator for the cert chain (but not the name).
		if o.sslCACert != "" {
			tlsCAder, _ := pem.Decode([]byte(o.sslCACert))
			if tlsCAder == nil {
				return nil, nil, fmt.Errorf("Couldn't parse CA certificate")
			}

			tlsCAcert, err := x509.ParseCertificate(tlsCAder.Bytes)
			if err != nil {
				return nil, nil, err
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

		options = append(options, ovsdbClient.WithTLSConfig(tlsConfig))
	}

	ovn, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, nil, err
	}

	err = ovn.Connect(context.Background())
	if err != nil {
		return nil, nil, err
	}

	err = ovn.Echo(context.TODO())
	if err != nil {
		return nil, nil, err
	}

	monitorCookie, err := ovn.MonitorAll(context.TODO())
	if err != nil {
		return nil, nil, err
	}

	dbClient := &ovnClient{
		client: ovn,
		group:  sync.WaitGroup{},
	}

	dbClient.group.Add(1)

	go func() {
		dbClient.group.Wait()
		_ = ovn.MonitorCancel(context.TODO(), monitorCookie)
	}()

	o.ovsdbClient[database] = dbClient

	return ovn, func() { dbClient.group.Done() }, nil
}

// getNorthboundDB returns connection string to use for northbound database.
func (o *OVN) getNorthboundDB() string {
	if o.nbDBAddr == "" {
		return "unix:/var/run/ovn/ovnnb_db.sock"
	}

	return o.nbDBAddr
}

// getSouthboundDB returns connection string to use for northbound database.
func (o *OVN) getSouthboundDB() string {
	if o.sbDBAddr == "" {
		return "unix:/var/run/ovn/ovnsb_db.sock"
	}

	return o.sbDBAddr
}

// sbctl executes ovn-sbctl with arguments to connect to wrapper's southbound database.
func (o *OVN) sbctl(args ...string) (string, error) {
	return o.xbctl(ovnDatabaseSouthbound, args...)
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *OVN) nbctl(args ...string) (string, error) {
	return o.xbctl(ovnDatabaseNorthbound, append([]string{"--wait=sb"}, args...)...)
}

// xbctl optionally executes either ovn-nbctl or ovn-sbctl with arguments to connect to wrapper's northbound or southbound database.
func (o *OVN) xbctl(database ovnDatabase, extraArgs ...string) (string, error) {
	// Figure out the command.
	var dbAddr string
	var cmd string
	if database == ovnDatabaseNorthbound {
		dbAddr = o.getNorthboundDB()
		cmd = "ovn-nbctl"
	} else if database == ovnDatabaseSouthbound {
		dbAddr = o.getSouthboundDB()
		cmd = "ovn-sbctl"
	} else {
		return "", fmt.Errorf("Unsupported database type %q", database)
	}

	// Figure out args.
	args := []string{"--timeout=10", "--db", dbAddr}

	// Handle SSL args.
	files := []*os.File{}
	if strings.Contains(dbAddr, "ssl:") {
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
	return subprocess.RunCommandInheritFds(context.Background(), files, cmd, args...)
}
