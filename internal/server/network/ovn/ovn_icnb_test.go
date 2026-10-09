package ovn

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	icmodel "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-ic-nb"
	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

func icTestServer(t *testing.T) (string, ovsdb.UUID) {
	t.Helper()
	server, err := exec.LookPath("ovsdb-server")
	if err != nil {
		t.Skip("ovsdb-server is required for real IC backend tests")
	}

	tool, err := exec.LookPath("ovsdb-tool")
	if err != nil {
		t.Skip("ovsdb-tool is required for real IC backend tests")
	}

	dir, err := os.MkdirTemp("", "ic-fence-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	schema, err := json.Marshal(icmodel.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command(tool, "create", dbPath, schemaPath).CombinedOutput()
	require.NoError(t, err, string(output))
	socket := filepath.Join(dir, "socket")
	var log bytes.Buffer
	cmd := exec.Command(server, dbPath, "--remote=punix:"+socket, "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--no-chdir")
	cmd.Stdout, cmd.Stderr = &log, &log
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	})
	require.Eventually(t, func() bool { _, err := os.Stat(socket); return err == nil }, 5*time.Second, 10*time.Millisecond)
	model, err := icmodel.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	client, err := ovsdbClient.NewOVSDBClient(model, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	ops := []ovsdb.Operation{{Op: ovsdb.OperationInsert, Table: "IC_NB_Global", Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}}}}
	reply, err := client.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	return socket, reply[0].UUID
}

// icTestDelayedDelete appends a real server wait and confirms dispatch with a same-connection echo.
func icTestDelayedDelete(t *testing.T, upstream string, root ovsdb.UUID) (string, *atomic.Bool, <-chan struct{}) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ic-proxy-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	armed := &atomic.Bool{}
	dispatched := make(chan struct{})
	var connectionsMu sync.Mutex
	connections := []net.Conn{}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		downstream, err := listener.Accept()
		if err != nil {
			return
		}

		backend, err := net.Dial("unix", upstream)
		if err != nil {
			_ = downstream.Close()
			return
		}

		connectionsMu.Lock()
		connections = append(connections, downstream, backend)
		connectionsMu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			decoder, encoder := json.NewDecoder(backend), json.NewEncoder(downstream)
			for {
				var frame map[string]json.RawMessage
				err := decoder.Decode(&frame)
				if err != nil {
					return
				}

				if string(frame["id"]) == `"ic-fence-test-dispatch"` {
					close(dispatched)
					continue
				}

				err = encoder.Encode(frame)
				if err != nil {
					return
				}
			}
		}()
		decoder, encoder := json.NewDecoder(downstream), json.NewEncoder(backend)
		for {
			var frame map[string]json.RawMessage
			err := decoder.Decode(&frame)
			if err != nil {
				return
			}

			held := false
			if string(frame["method"]) == `"transact"` && armed.Load() {
				var params []json.RawMessage
				err = json.Unmarshal(frame["params"], &params)
				if err != nil {
					return
				}

				for _, parameter := range params[1:] {
					var operation ovsdb.Operation
					err = json.Unmarshal(parameter, &operation)
					if err == nil && operation.Op == ovsdb.OperationDelete && operation.Table == "Transit_Switch" {
						held = armed.CompareAndSwap(true, false)
						break
					}
				}

				if held {
					timeout := 15000
					wait := ovsdb.Operation{Op: ovsdb.OperationWait, Table: "IC_NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}, {Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{"ic-test-latch": "release"}}}}, Columns: []string{"_uuid"}, Rows: []ovsdb.Row{{"_uuid": root}}, Until: "==", Timeout: &timeout}
					payload, _ := json.Marshal(wait)
					params = append(params, payload)
					frame["params"], _ = json.Marshal(params)
				}
			}

			err = encoder.Encode(frame)
			if err != nil {
				return
			}

			if held {
				err = encoder.Encode(map[string]any{"method": "echo", "params": []any{}, "id": "ic-fence-test-dispatch"})
				if err != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		connectionsMu.Lock()
		for _, connection := range connections {
			_ = connection.Close()
		}

		connectionsMu.Unlock()
		workers.Wait()
	})
	return socket, armed, dispatched
}

func TestICNBFenceRejectsDispatchedOldDelete(t *testing.T) {
	address, root := icTestServer(t)
	proxy, armed, dispatched := icTestDelayedDelete(t, address, root)
	old, err := NewICNB("unix:"+proxy, "", "", "", "ic-member")
	require.NoError(t, err)
	t.Cleanup(old.client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.Equal(t, root.GoUUID, old.BackendID())
	require.NoError(t, old.CreateTransitSwitch(ctx, "retained", false))
	armed.Store(true)
	done := make(chan error, 1)
	go func() { done <- old.DeleteTransitSwitch(ctx, "retained", false) }()
	select {
	case <-dispatched:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	select {
	case err := <-done:
		t.Fatalf("Old delete completed before backend fencing: %v", err)
	default:
	}

	fresh, err := NewICNB("unix:"+address, "", "", "", "ic-member")
	require.NoError(t, err)
	t.Cleanup(fresh.client.Close)
	require.Equal(t, root.GoUUID, fresh.BackendID())
	select {
	case err := <-done:
		require.ErrorIs(t, err, backendDB.ErrFenced)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	row := &icmodel.TransitSwitch{Name: "retained"}
	require.NoError(t, fresh.client.Get(ctx, row))
	require.NotEmpty(t, row.UUID)
	require.NoError(t, fresh.DeleteTransitSwitch(ctx, "retained", false))
	require.NoError(t, fresh.CreateTransitSwitch(ctx, "retained", false))
	replacement := &icmodel.TransitSwitch{Name: "retained"}
	require.NoError(t, fresh.client.Get(ctx, replacement))
	require.NotEqual(t, row.UUID, replacement.UUID)
	require.Eventually(t, func() bool {
		cached := &icmodel.TransitSwitch{Name: "retained"}
		err := old.client.Get(ctx, cached)
		return err == nil && cached.UUID == replacement.UUID
	}, time.Second, time.Millisecond)
	require.ErrorIs(t, old.DeleteTransitSwitch(ctx, "retained", true), backendDB.ErrFenced)
	require.NoError(t, fresh.client.Get(ctx, replacement))
}
