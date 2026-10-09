package ovn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	ovnNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// NB client.
type NB struct {
	client      ovsdbClient.Client
	cookie      ovsdbClient.MonitorCookie
	backendID   string
	tunnelPorts map[OVNSwitchPort]bool
}

// Close releases this client's connections and monitors.
func (o *NB) Close() { o.client.Close() }

// BackendID returns the root UUID acknowledged at construction, or empty for an unfenced client.
func (o *NB) BackendID() string {
	return o.backendID
}

// NewNB initializes new OVN client for Northbound operations.
func NewNB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string, owner ...string) (*NB, error) {
	return newNB(dbAddr, sslCACert, sslClientCert, sslClientKey, nil, owner...)
}

// NewNBWithRootAdmission binds durable applicability before constructor fencing writes.
func NewNBWithRootAdmission(dbAddr, sslCACert, sslClientCert, sslClientKey, owner string, admit func(context.Context, string) error) (*NB, error) {
	if admit == nil {
		return nil, errors.New("NB root admission callback is required")
	}

	return newNB(dbAddr, sslCACert, sslClientCert, sslClientKey, admit, owner)
}

func newNB(dbAddr string, sslCACert string, sslClientCert string, sslClientKey string, admit func(context.Context, string) error, owner ...string) (*NB, error) {
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

	monitorCookie, err := ovn.MonitorAll(ctx)
	if err != nil {
		return nil, err
	}

	backend := ovn
	if admit != nil {
		result, e := ovn.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "NB_Global", Where: []ovsdb.Condition{}})
		if e != nil {
			ovn.Close()
			return nil, e
		}

		if len(result) != 1 || len(result[0].Rows) != 1 {
			ovn.Close()
			return nil, errors.New("NB root admission requires exactly one original NB_Global")
		}

		root, e := nicCleanupRowUUID(result[0].Rows[0], "_uuid")
		if e != nil {
			ovn.Close()
			return nil, e
		}

		e = admit(ctx, root)
		if e != nil {
			ovn.Close()
			return nil, e
		}

		backend = &admittedNBClient{Client: ovn, root: root, invalidate: cancel}
	}

	if len(owner) > 0 && owner[0] != "" {
		fenced, err := backendDB.NewFencedClient(ctx, backend, "NB_Global", owner[0])
		if err != nil {
			ovn.Close()
			return nil, err
		}

		backend = fenced
		client.backendID = fenced.RootUUID()
	}

	// Add the client to the struct.
	client.client = &timeoutClient{Client: backend, name: "northbound"}
	client.cookie = monitorCookie

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *NB) {
		_ = ovn.MonitorCancel(context.Background(), o.cookie)
		ovn.Close()
	})

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
		return errors.New("Bad collection type")
	}

	rVal = rVal.Elem()
	if rVal.Kind() != reflect.Slice {
		return errors.New("Bad collection type")
	}

	if rVal.Len() == 0 {
		return ovsdbClient.ErrNotFound
	}

	if rVal.Len() > 1 {
		return ErrTooMany
	}

	reflect.ValueOf(m).Elem().Set(rVal.Index(0))
	return nil
}

// admittedNBClient pins the same root even if NB_Global changes before constructor fencing.
type admittedNBClient struct {
	ovsdbClient.Client
	root       string
	invalidate context.CancelFunc
}

// Transact checks durable reference admission before backend effects.
func (c *admittedNBClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	mutation := false
	for _, op := range ops {
		if op.Op == ovsdb.OperationInsert || op.Op == ovsdb.OperationUpdate || op.Op == ovsdb.OperationMutate || op.Op == ovsdb.OperationDelete {
			mutation = true
			break
		}
	}

	guard := nicCleanupRootWait(c.root)
	// Check reads too: the constructor must not adopt a different singleton then repeatedly retry writes.
	if !mutation {
		results, err := c.Client.Transact(ctx, append([]ovsdb.Operation{guard}, ops...)...)
		if err != nil {
			return nil, err
		}

		if len(results) < 1 {
			return nil, errors.New("NB root admission response is incomplete")
		}

		_, err = ovsdb.CheckOperationResults(results[:1], []ovsdb.Operation{guard})
		if err != nil {
			c.invalidate()
			return nil, err
		}

		return results[1:], nil
	}

	results, err := c.Client.Transact(ctx, append([]ovsdb.Operation{guard}, ops...)...)
	if err != nil {
		return nil, err
	}

	if len(results) < 1 {
		return nil, errors.New("NB root admission response is incomplete")
	}

	_, err = ovsdb.CheckOperationResults(results[:1], []ovsdb.Operation{guard})
	if err != nil {
		c.invalidate()
		return nil, err
	}

	return results[1:], nil
}
