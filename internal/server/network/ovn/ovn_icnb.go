package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"

	ovnICNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-ic-nb"
	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// ICNB client.
type ICNB struct {
	client    ovsdbClient.Client
	cookie    ovsdbClient.MonitorCookie
	backendID string
}

// NewICNB initializes new OVN client for Northbound IC operations.
func NewICNB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string, owner ...string) (*ICNB, error) {
	// Create the NB struct.
	client := &ICNB{}

	// Prepare the OVSDB client.
	dbSchema, err := ovnICNB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard), ovsdbClient.WithInactivityCheck(20*time.Second, 5*time.Second, &backoff.ZeroBackOff{})}
	for entry := range strings.SplitSeq(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// Handle SSL.
	if strings.Contains(dbAddr, "ssl:") {
		// Validation.
		if sslClientCert == "" {
			return nil, errors.New("OVN IC Northbound database is configured to use SSL but no client certificate was found")
		}

		if sslClientKey == "" {
			return nil, errors.New("OVN IC Northbound database is configured to use SSL but no client key was found")
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
			clientCAPool := x509.NewCertPool()
			ok := clientCAPool.AppendCertsFromPEM([]byte(sslCACert))
			if !ok {
				return nil, errors.New("Invalid CA")
			}

			tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
				if len(rawCerts) < 1 {
					return errors.New("Missing server certificate")
				}

				// Load the chain.
				intermediates := x509.NewCertPool()
				if len(rawCerts) > 1 {
					for _, rawCert := range rawCerts[1:] {
						cert, _ := x509.ParseCertificate(rawCert)
						if cert != nil {
							intermediates.AddCert(cert)
						}
					}
				}

				// Load the main server certificate.
				cert, _ := x509.ParseCertificate(rawCerts[0])
				if cert == nil {
					return errors.New("Bad server certificate")
				}

				// Validate.
				opts := x509.VerifyOptions{
					Roots:         clientCAPool,
					Intermediates: intermediates,
				}

				_, err := cert.Verify(opts)
				return err
			}
		}

		// Add the TLS config to the client.
		options = append(options, ovsdbClient.WithTLSConfig(tlsConfig))
	}

	// Connect to OVSDB.
	ovn, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, err
	}

	// Bound the initial connection and monitor setup.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = ovn.Connect(ctx)
	if err != nil {
		return nil, err
	}

	err = ovn.Echo(ctx)
	if err != nil {
		return nil, err
	}

	monitorCookie, err := ovn.MonitorAll(ctx)
	if err != nil {
		return nil, err
	}

	backend := ovn
	if len(owner) > 0 && owner[0] != "" {
		fenced, err := backendDB.NewFencedClient(ctx, ovn, "IC_NB_Global", owner[0])
		if err != nil {
			ovn.Close()
			return nil, err
		}

		backend = fenced
		client.backendID = fenced.RootUUID()
	}

	// Add the client to the struct.
	client.client = &timeoutClient{Client: backend, name: "interconnect northbound", uncertainWrites: client.backendID == ""}
	client.cookie = monitorCookie

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *ICNB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

	return client, nil
}

// BackendID returns the root identity whose member generation was acknowledged.
func (o *ICNB) BackendID() string {
	return o.backendID
}
