package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-org/libovsdb/client"
	ovsdbModel "github.com/ovn-org/libovsdb/model"

	ovnNB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-nb"
)

// NB client.
type NB struct {
	client ovsdbClient.Client
	cookie ovsdbClient.MonitorCookie
}

var nb *NB

// NewNB initializes new OVN client for Northbound operations.
func NewNB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string) (*NB, error) {
	if nb != nil {
		return nb, nil
	}

	// Create the NB struct.
	client := &NB{}

	// Prepare the OVSDB client.
	dbSchema, err := ovnNB.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	// Add some missing indexes.
	dbSchema.SetIndexes(map[string][]ovsdbModel.ClientIndex{
		"Load_Balancer":       {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Router":      {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Switch":      {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
		"Logical_Switch_Port": {{Columns: []ovsdbModel.ColumnKey{{Column: "name"}}}},
	})

	discard := logr.Discard()

	options := []ovsdbClient.Option{ovsdbClient.WithLogger(&discard), ovsdbClient.WithReconnect(5*time.Second, &backoff.ZeroBackOff{})}
	for _, entry := range strings.Split(dbAddr, ",") {
		options = append(options, ovsdbClient.WithEndpoint(entry))
	}

	// Handle SSL.
	if strings.Contains(dbAddr, "ssl:") {
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
		ovn.Close()
	})

	nb = client
	return client, nil
}

// get is used to perform a libovsdb Get call while also makes use of the custom defined index.
// For some reason the main Get() function only uses the built-in indices rather than considering the user provided ones.
// This is apparently by design but makes it much more annoying to fetch records from some tables.
func (o *NB) get(ctx context.Context, m ovsdbModel.Model) error {
	var collection any

	// Check if one of the broken types.
	switch m.(type) {
	case *ovnNB.LoadBalancer:
		s := []ovnNB.LoadBalancer{}
		collection = &s
	case *ovnNB.LogicalRouter:
		s := []ovnNB.LogicalRouter{}
		collection = &s
	case *ovnNB.LogicalSwitch:
		s := []ovnNB.LogicalSwitch{}
		collection = &s
	case *ovnNB.LogicalSwitchPort:
		s := []ovnNB.LogicalSwitchPort{}
		collection = &s
	default:
		// Fallback to normal Get.
		return o.client.Get(ctx, m)
	}

	// Check and assign the resulting value.
	err := o.client.Where(m).List(ctx, collection)
	if err != nil {
		return err
	}

	rVal := reflect.ValueOf(collection)
	if rVal.Kind() != reflect.Pointer {
		return fmt.Errorf("Bad collection type")
	}

	rVal = rVal.Elem()
	if rVal.Kind() != reflect.Slice {
		return fmt.Errorf("Bad collection type")
	}

	if rVal.Len() != 1 {
		return ovsdbClient.ErrNotFound
	}

	reflect.ValueOf(m).Elem().Set(rVal.Index(0))
	return nil
}
