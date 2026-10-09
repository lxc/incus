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
	ovsdbCache "github.com/ovn-kubernetes/libovsdb/cache"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	ovnSB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-sb"
)

// SB client.
type SB struct {
	client    ovsdbClient.Client
	cookie    ovsdbClient.MonitorCookie
	backendID string
	owner     string
}

// BackendID returns the database root used to constrain conditional cache invalidations.
func (o *SB) BackendID() string {
	return o.backendID
}

// NewSB initializes new OVN client for Southbound operations.
func NewSB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string, owner ...string) (*SB, error) {
	// Prepare the OVSDB client.
	dbSchema, err := ovnSB.FullDatabaseModel()
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
			return nil, errors.New("OVN is configured to use SSL but no client certificate was found")
		}

		if sslClientKey == "" {
			return nil, errors.New("OVN is configured to use SSL but no client key was found")
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

	// Set up monitor for the tables we use.
	monitorCookie, err := ovn.Monitor(ctx, ovn.NewMonitor(
		ovsdbClient.WithTable(&ovnSB.Chassis{}),
		ovsdbClient.WithTable(&ovnSB.PortBinding{}),
		ovsdbClient.WithTable(&ovnSB.ServiceMonitor{}),
	))
	if err != nil {
		return nil, err
	}

	// Set up event handlers.
	eventHandler := &ovsdbCache.EventHandlerFuncs{}
	eventHandler.AddFunc = func(table string, newModel ovsdbModel.Model) {
		dispatchOVNSBEvent("add", table, nil, newModel)
	}

	eventHandler.UpdateFunc = func(table string, oldModel ovsdbModel.Model, newModel ovsdbModel.Model) {
		dispatchOVNSBEvent("update", table, oldModel, newModel)
	}

	eventHandler.DeleteFunc = func(table string, oldModel ovsdbModel.Model) {
		dispatchOVNSBEvent("remove", table, oldModel, nil)
	}

	ovn.Cache().AddEventHandler(eventHandler)

	// Southbound only invalidates exact MAC cache row versions; it needs no metadata write privilege.
	ops := []ovsdb.Operation{{Op: ovsdb.OperationSelect, Table: "SB_Global", Where: []ovsdb.Condition{}, Columns: []string{"_uuid"}}}
	reply, err := ovn.Transact(ctx, ops...)
	if err != nil {
		ovn.Close()
		return nil, err
	}

	_, err = ovsdb.CheckOperationResults(reply, ops)
	if err != nil {
		ovn.Close()
		return nil, err
	}

	if len(reply) != 1 || len(reply[0].Rows) != 1 {
		ovn.Close()
		return nil, errors.New("Expected one OVN southbound database root")
	}

	root, ok := reply[0].Rows[0]["_uuid"].(ovsdb.UUID)
	if !ok || root.GoUUID == "" {
		ovn.Close()
		return nil, errors.New("Invalid OVN southbound database root")
	}

	// Create the SB struct.
	client := &SB{
		client:    &timeoutClient{Client: ovn, name: "southbound"},
		cookie:    monitorCookie,
		backendID: root.GoUUID,
	}

	if len(owner) > 0 {
		client.owner = owner[0]
	}

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *SB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

	return client, nil
}
