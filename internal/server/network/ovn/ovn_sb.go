package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	ovsdbCache "github.com/ovn-org/libovsdb/cache"
	ovsdbClient "github.com/ovn-org/libovsdb/client"
	ovsdbModel "github.com/ovn-org/libovsdb/model"

	ovnSB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-sb"
)

// SB client.
type SB struct {
	client ovsdbClient.Client
	cookie ovsdbClient.MonitorCookie
}

// NewSB initializes new OVN client for Southbound operations.
func NewSB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string) (*SB, error) {
	// Prepare the OVSDB client.
	dbSchema, err := ovnSB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard), ovsdbClient.WithReconnect(5*time.Second, &backoff.ZeroBackOff{})}
	for _, entry := range strings.Split(dbAddr, ",") {
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
			tlsCAder, _ := pem.Decode([]byte(sslCACert))
			if tlsCAder == nil {
				return nil, errors.New("Couldn't parse CA certificate")
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
					return errors.New("Missing server certificate")
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
					return errors.New("Bad server certificate")
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

	// Set up monitor for the tables we use.
	monitorCookie, err := ovn.Monitor(context.TODO(), ovn.NewMonitor(
		ovsdbClient.WithTable(&ovnSB.Chassis{}),
		ovsdbClient.WithTable(&ovnSB.PortBinding{}),
		ovsdbClient.WithTable(&ovnSB.ServiceMonitor{})))
	if err != nil {
		return nil, err
	}

	// Set up event handlers.
	eventHandler := &ovsdbCache.EventHandlerFuncs{}
	eventHandler.AddFunc = func(table string, newModel ovsdbModel.Model) {
		sbEventHandlersMu.Lock()
		defer sbEventHandlersMu.Unlock()

		if sbEventHandlers == nil {
			return
		}

		for _, handler := range sbEventHandlers {
			if handler.Hook != nil && slices.Contains(handler.Tables, table) {
				go handler.Hook("add", table, nil, newModel)
			}
		}
	}

	eventHandler.UpdateFunc = func(table string, oldModel ovsdbModel.Model, newModel ovsdbModel.Model) {
		sbEventHandlersMu.Lock()
		defer sbEventHandlersMu.Unlock()

		if sbEventHandlers == nil {
			return
		}

		for _, handler := range sbEventHandlers {
			if handler.Hook != nil && slices.Contains(handler.Tables, table) {
				go handler.Hook("update", table, oldModel, newModel)
			}
		}
	}

	eventHandler.DeleteFunc = func(table string, oldModel ovsdbModel.Model) {
		sbEventHandlersMu.Lock()
		defer sbEventHandlersMu.Unlock()

		if sbEventHandlers == nil {
			return
		}

		for _, handler := range sbEventHandlers {
			if handler.Hook != nil && slices.Contains(handler.Tables, table) {
				go handler.Hook("remove", table, oldModel, nil)
			}
		}
	}

	ovn.Cache().AddEventHandler(eventHandler)

	// Create the SB struct.
	client := &SB{
		client: ovn,
		cookie: monitorCookie,
	}

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *SB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

	return client, nil
}
