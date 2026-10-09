package ovn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovnSB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-sb"
)

type sbActionTestServer struct {
	admin      ovsdbClient.Client
	client     *SB
	root       ovsdb.UUID
	datapath   ovsdb.UUID
	connection ovsdb.UUID
	logPath    string
}

func sbActionTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sb-actions-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func sbActionTransact(t *testing.T, client ovsdbClient.Client, operations ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reply, err := client.Transact(ctx, operations...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, operations)
	require.NoError(t, err, "%+v", reply)
	return reply
}

func newSBActionTestServer(t *testing.T) *sbActionTestServer {
	t.Helper()
	server, err := exec.LookPath("ovsdb-server")
	if err != nil {
		t.Skip("ovsdb-server is required for real Southbound transaction tests")
	}

	tool, err := exec.LookPath("ovsdb-tool")
	if err != nil {
		t.Skip("ovsdb-tool is required for real Southbound transaction tests")
	}

	dir, err := os.MkdirTemp("", "ovn-sb-actions-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	schema, err := json.Marshal(ovnSB.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command(tool, "create", dbPath, schemaPath).CombinedOutput()
	require.NoError(t, err, string(output))
	adminSocket := filepath.Join(dir, "admin")
	certificate, key := sbActionTestCertificate(t)
	certPath := filepath.Join(dir, "test.crt")
	keyPath := filepath.Join(dir, "test.key")
	require.NoError(t, os.WriteFile(certPath, []byte(certificate), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte(key), 0o600))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	logPath := filepath.Join(dir, "server.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(server, dbPath, "--remote=punix:"+adminSocket, "--remote=db:OVN_Southbound,SB_Global,connections", "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--no-chdir", "--verbose=jsonrpc:dbg", "--private-key="+keyPath, "--certificate="+certPath, "--ca-cert="+certPath)
	cmd.Stdout = log
	cmd.Stderr = log
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Log(string(data))
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(adminSocket)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	model, err := ovnSB.FullDatabaseModel()
	require.NoError(t, err)
	logger := logr.Discard()
	admin, err := ovsdbClient.NewOVSDBClient(model, ovsdbClient.WithEndpoint("unix:"+adminSocket), ovsdbClient.WithLogger(&logger))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, admin.Connect(ctx))
	t.Cleanup(admin.Close)

	// The controller role may change MAC bindings but has no SB_Global write permission.
	reply := sbActionTransact(
		t, admin,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "RBAC_Permission", UUIDName: "mac_permission", Row: ovsdb.Row{"table": "MAC_Binding", "authorization": ovsdb.OvsSet{GoSet: []any{""}}, "insert_delete": true, "update": ovsdb.OvsSet{GoSet: []any{"logical_port", "ip", "mac", "datapath", "timestamp"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "RBAC_Role", Row: ovsdb.Row{"name": "ovn-controller", "permissions": ovsdb.OvsMap{GoMap: map[any]any{"MAC_Binding": ovsdb.UUID{GoUUID: "mac_permission"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Connection", UUIDName: "restricted", Row: ovsdb.Row{"target": "pssl:" + port + ":127.0.0.1", "role": "ovn-controller"}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "SB_Global", Row: ovsdb.Row{"connections": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "restricted"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Datapath_Binding", Row: ovsdb.Row{"tunnel_key": 1}},
	)
	require.Eventually(t, func() bool {
		connection, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if err != nil {
			return false
		}

		_ = connection.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)
	client, err := NewSB("ssl:127.0.0.1:"+port, certificate, certificate, key, "sb-actions-test")
	require.NoError(t, err)
	t.Cleanup(func() {
		runtime.SetFinalizer(client, nil)
		client.client.Close()
	})
	require.Equal(t, reply[3].UUID.GoUUID, client.BackendID())
	return &sbActionTestServer{admin: admin, client: client, connection: reply[2].UUID, root: reply[3].UUID, datapath: reply[4].UUID, logPath: logPath}
}

func (s *sbActionTestServer) addBinding(t *testing.T, port string, ip string) ovsdb.Row {
	t.Helper()
	sbActionTransact(t, s.admin, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "MAC_Binding", Row: ovsdb.Row{"logical_port": port, "ip": ip, "mac": "00:16:3e:00:00:01", "timestamp": 1, "datapath": s.datapath}})
	rows := s.bindings(t, port)
	require.Len(t, rows, 1)
	return rows[0]
}

func (s *sbActionTestServer) bindings(t *testing.T, port string) []ovsdb.Row {
	t.Helper()
	reply := sbActionTransact(t, s.admin, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "MAC_Binding", Where: []ovsdb.Condition{{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: port}}, Columns: []string{"_uuid", "_version", "mac", "ip", "datapath", "timestamp"}})
	return reply[0].Rows
}

// sbActionWaitClient appends a server-side wait to the actual production delete transaction.
type sbActionWaitClient struct {
	ovsdbClient.Client
	operations chan []ovsdb.Operation
}

func (c *sbActionWaitClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, operation := range operations {
		if operation.Op == ovsdb.OperationDelete && operation.Table == "MAC_Binding" {
			c.operations <- operations
			serverTimeout := 10000
			operations = append(append([]ovsdb.Operation{}, operations...), ovsdb.Operation{Op: ovsdb.OperationWait, Table: "SB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionNotEqual, Value: ovsdb.UUID{GoUUID: "00000000-0000-0000-0000-000000000000"}}}, Columns: []string{"nb_cfg"}, Until: "==", Rows: []ovsdb.Row{{"nb_cfg": 1}}, Timeout: &serverTimeout})
			reply, err := c.Client.Transact(ctx, operations...)
			if err == nil && len(reply) == len(operations) {
				// The injected wait is a test transport constraint, not part of the method result.
				reply = reply[:len(reply)-1]
			}

			return reply, err
		}
	}

	return c.Client.Transact(ctx, operations...)
}

func (s *sbActionTestServer) waitForDelete(t *testing.T, client *sbActionWaitClient, row ovsdb.Row) {
	t.Helper()
	select {
	case operations := <-client.operations:
		require.Equal(t, "SB_Global", operations[0].Table)
		require.Equal(t, ovsdb.OperationWait, operations[0].Op)
		require.Equal(t, operations[0], operations[len(operations)-1])
		for _, operation := range operations {
			if operation.Op == ovsdb.OperationDelete {
				require.Contains(t, operation.Where, ovsdb.Condition{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]})
				require.Contains(t, operation.Where, ovsdb.Condition{Column: "mac", Function: ovsdb.ConditionEqual, Value: row["mac"]})
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Production MAC binding delete was not constructed")
	}

	// A received JSON-RPC trace and a later echo establish dispatch before competing writes.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(s.logPath)
		if err != nil {
			return false
		}

		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.Contains(line, "received request") && strings.Contains(line, "incus:ovn-lifecycle:sb-actions-test") && strings.Contains(line, `"op":"delete"`) {
				return true
			}
		}

		return false
	}, 5*time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Echo(ctx))
}

func TestSBDeleteMACBindingsRestrictedRole(t *testing.T) {
	s := newSBActionTestServer(t)
	s.addBinding(t, "router-port", "192.0.2.1")
	ops := []ovsdb.Operation{{Op: ovsdb.OperationUpdate, Table: "SB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: s.root}}, Row: ovsdb.Row{"nb_cfg": 99}}}
	reply, err := s.client.client.Transact(context.Background(), ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.Error(t, err)
	require.Equal(t, "permission error", reply[0].Error)
	require.NoError(t, s.client.DeleteMACBindings(context.Background(), "router-port", net.ParseIP("192.0.2.1")))
	require.Empty(t, s.bindings(t, "router-port"))
}

func TestSBDeleteMACBindingsDispatchedVersionChecks(t *testing.T) {
	for _, action := range []string{"unchanged", "changed version", "replacement UUID", "changed root", "caller deadline"} {
		t.Run(action, func(t *testing.T) {
			s := newSBActionTestServer(t)
			row := s.addBinding(t, "router-port", "192.0.2.1")
			foreign := s.addBinding(t, "other-port", "192.0.2.1")
			delayed := &sbActionWaitClient{Client: s.client.client, operations: make(chan []ovsdb.Operation, 1)}
			s.client.client = delayed
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if action == "caller deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()
			}

			done := make(chan error, 1)
			go func() { done <- s.client.DeleteMACBindings(ctx, "router-port", net.ParseIP("192.0.2.1")) }()
			s.waitForDelete(t, delayed, row)
			require.Equal(t, []ovsdb.Row{row}, s.bindings(t, "router-port"))
			expected := []ovsdb.Row{}
			switch action {
			case "changed version":
				sbActionTransact(t, s.admin, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "MAC_Binding", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"mac": "00:16:3e:00:00:02", "timestamp": 2}})
				expected = s.bindings(t, "router-port")
				require.Equal(t, row["_uuid"], expected[0]["_uuid"])
				require.NotEqual(t, row["_version"], expected[0]["_version"])
			case "replacement UUID":
				sbActionTransact(t, s.admin, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "MAC_Binding", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}})
				replacement := s.addBinding(t, "router-port", "192.0.2.1")
				require.NotEqual(t, row["_uuid"], replacement["_uuid"])
				expected = []ovsdb.Row{replacement}
			case "changed root":
				sbActionTransact(
					t, s.admin,
					ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "SB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: s.root}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "SB_Global", Row: ovsdb.Row{"connections": ovsdb.OvsSet{GoSet: []any{s.connection}}}},
				)
				expected = []ovsdb.Row{row}
			case "caller deadline":
				select {
				case err := <-done:
					require.ErrorIs(t, err, context.DeadlineExceeded)
				case <-time.After(5 * time.Second):
					t.Fatal("Caller did not retain its deadline error")
				}

				require.Equal(t, []ovsdb.Row{row}, s.bindings(t, "router-port"))
			}

			sbActionTransact(t, s.admin, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "SB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionNotEqual, Value: ovsdb.UUID{GoUUID: "00000000-0000-0000-0000-000000000000"}}}, Row: ovsdb.Row{"nb_cfg": 1}})
			if action != "caller deadline" {
				select {
				case err := <-done:
					if action == "changed root" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}

				case <-time.After(5 * time.Second):
					t.Fatal("Submitted delete did not finish after releasing the server wait")
				}
			}

			require.Eventually(t, func() bool {
				return len(s.bindings(t, "router-port")) == len(expected)
			}, 5*time.Second, 10*time.Millisecond)
			require.Equal(t, expected, s.bindings(t, "router-port"))
			require.Equal(t, []ovsdb.Row{foreign}, s.bindings(t, "other-port"))
		})
	}
}
