package ovs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovsmodel "github.com/lxc/incus/v7/internal/server/network/ovs/schema/ovs"
	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

type nicOVSRecordedClient struct {
	ovsdbClient.Client
	t           *testing.T
	file        *os.File
	beforeWrite func()
	loseReply   bool
}

func (c *nicOVSRecordedClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	raw, err := json.Marshal(ops)
	require.NoError(c.t, err)
	_, err = c.file.Write(append(raw, '\n'))
	require.NoError(c.t, err)
	writing := slices.ContainsFunc(ops, func(op ovsdb.Operation) bool { return op.Op == ovsdb.OperationDelete })
	if writing && c.beforeWrite != nil {
		hook := c.beforeWrite
		c.beforeWrite = nil
		hook()
	}

	results, err := c.Client.Transact(ctx, ops...)
	if writing && c.loseReply && err == nil {
		c.loseReply = false
		return nil, errors.New("injected lost committed OVS reply")
	}

	return results, err
}

func nicOVSBackend(t *testing.T) (*VSwitch, ovsdbClient.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "oc-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schema, err := json.Marshal(ovsmodel.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command("/usr/bin/ovsdb-tool", "create", dbPath, schemaPath).CombinedOutput()
	require.NoError(t, err, string(output))
	socket := filepath.Join(dir, "socket")
	logPath := filepath.Join(dir, "process-log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logFile.Close()) })
	logOutput := func() string {
		data, err := os.ReadFile(logPath)
		require.NoError(t, err)
		return string(data)
	}

	cmd := exec.Command("/usr/sbin/ovsdb-server", dbPath, "--remote=punix:"+socket, "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--log-file="+filepath.Join(dir, "log"), "--no-chdir")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			require.Error(t, err, logOutput())
			require.Equal(t, syscall.SIGTERM, cmd.ProcessState.Sys().(syscall.WaitStatus).Signal())
			t.Logf("owned ovsdb-server PID=%d reaped after requested SIGTERM; root path %s", cmd.Process.Pid, dir)
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("owned ovsdb-server required forced teardown")
		}

		require.NotNil(t, cmd.ProcessState)
	})
	require.Eventually(t, func() bool { _, err := os.Stat(socket); return err == nil }, 5*time.Second, 10*time.Millisecond, logOutput())
	m, err := ovsmodel.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	nicOVSBackendExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Open_vSwitch", Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}}})
	nb, err := NewVSwitch("unix:"+socket, "nic-source-fixture")
	require.NoError(t, err)
	t.Cleanup(nb.client.Close)
	t.Logf("owned Unix-only OVS fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw
}

func nicOVSBackendExec(t *testing.T, c ovsdbClient.Client, ops ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(result, ops)
	require.NoError(t, err, "results: %#v", result)
	return result
}

func nicOVSBackendRows(t *testing.T, raw ovsdbClient.Client) map[string][]ovsdb.Row {
	t.Helper()
	rows := map[string][]ovsdb.Row{}
	for _, table := range []string{"Open_vSwitch", "Bridge", "Port", "Interface"} {
		result := nicOVSBackendExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}})
		rows[table] = result[0].Rows
		slices.SortFunc(rows[table], func(a, b ovsdb.Row) int {
			return strings.Compare(a["_uuid"].(ovsdb.UUID).GoUUID, b["_uuid"].(ovsdb.UUID).GoUUID)
		})
	}

	return rows
}

func TestNICOVSRealBackendLifecycle(t *testing.T) {
	for _, mode := range []string{"stop-replay-replacement", "changed-binding", "moved-parent", "root-cardinality", "wrong-root", "dispatch-race", "lost-committed-reply"} {
		t.Run(mode, func(t *testing.T) {
			o, raw := nicOVSBackend(t)
			evidence := os.Getenv("INCUS_NIC_PREFIX_EVIDENCE")
			if evidence == "" {
				evidence = t.TempDir()
			}

			file, err := os.Create(filepath.Join(evidence, "ovs-"+mode+"-operations.jsonl"))
			require.NoError(t, err)
			defer file.Close()
			fenced, validFenced := o.client.(*backendDB.FencedClient)
			require.True(t, validFenced)
			recorder := &nicOVSRecordedClient{Client: fenced.Client, t: t, file: file}
			fenced.Client = recorder
			nicOVSBackendExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "iface", Row: ovsdb.Row{"name": "original-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "original-ovn"}}}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "port", Row: ovsdb.Row{"name": "original-host", "interfaces": nicPortSet("iface")}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "siblingIface", Row: ovsdb.Row{"name": "sibling-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "sibling-ovn"}}}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "siblingPort", Row: ovsdb.Row{"name": "sibling-host", "interfaces": nicPortSet("siblingIface")}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Bridge", UUIDName: "bridge", Row: ovsdb.Row{"name": "br-original", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}, ovsdb.UUID{GoUUID: "siblingPort"}}}}},
				ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Open_vSwitch", Where: nicPortUUIDWhere(o.BackendID()), Mutations: []ovsdb.Mutation{{Column: "bridges", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSet("bridge")}}})
			plan, err := o.CaptureNICPortCleanup(context.Background(), "br-original", "original-host", "original-ovn")
			require.NoError(t, err)
			changeBinding := func() {
				nicOVSBackendExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Interface", Where: nicPortUUIDWhere(plan.InterfaceUUID), Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "replacement-ovn"}}}})
			}

			switch mode {
			case "changed-binding":
				changeBinding()
			case "moved-parent":
				nicOVSBackendExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Bridge", UUIDName: "movedBridge", Row: ovsdb.Row{"name": "br-moved", "ports": nicPortSet(plan.PortUUID)}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Open_vSwitch", Where: nicPortUUIDWhere(o.BackendID()), Mutations: []ovsdb.Mutation{{Column: "bridges", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSet("movedBridge")}}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Bridge", Where: nicPortUUIDWhere(plan.BridgeUUID), Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationDelete, Value: nicPortSet(plan.PortUUID)}}})

			case "root-cardinality":
				nicOVSBackendExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Open_vSwitch", Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{"foreign": "root"}}}})
			case "wrong-root":
				plan.RootUUID = plan.BridgeUUID
			case "dispatch-race":
				recorder.beforeWrite = changeBinding
			case "lost-committed-reply":
				recorder.loseReply = true
			}

			before := nicOVSBackendRows(t, raw)
			err = o.ApplyNICPortCleanup(context.Background(), plan)
			if mode == "stop-replay-replacement" || mode == "lost-committed-reply" {
				if mode == "stop-replay-replacement" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "lost committed OVS reply")
				}

				after := nicOVSBackendRows(t, raw)
				require.Len(t, after["Port"], 1)
				require.Equal(t, "sibling-host", after["Port"][0]["name"])
				require.Len(t, after["Interface"], 1)
				require.Equal(t, "sibling-host", after["Interface"][0]["name"])
				require.NoError(t, o.ApplyNICPortCleanup(context.Background(), plan))
				nicOVSBackendExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "newIface", Row: ovsdb.Row{"name": "original-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "new-ovn"}}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "newPort", Row: ovsdb.Row{"name": "original-host", "interfaces": nicPortSet("newIface")}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Bridge", Where: nicPortUUIDWhere(plan.BridgeUUID), Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSet("newPort")}}})
				before = nicOVSBackendRows(t, raw)
				require.NoError(t, o.ApplyNICPortCleanup(context.Background(), plan))
				require.Equal(t, before, nicOVSBackendRows(t, raw))
			} else {
				require.Error(t, err)
				if mode == "dispatch-race" {
					before["Interface"] = nicOVSBackendRows(t, raw)["Interface"]
				}

				require.Equal(t, before, nicOVSBackendRows(t, raw))
			}
		})
	}
}

// TestNICOVSRealBackendAbsence covers the rooted proof that no interface remains bound to
// an original OVN port whose host link is already gone. It never deletes any row.
func TestNICOVSRealBackendAbsence(t *testing.T) {
	for _, mode := range []string{"absent-replay", "still-bound", "bound-after-capture", "bridge-unrooted", "wrong-root", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			o, raw := nicOVSBackend(t)
			nicOVSBackendExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "siblingIface", Row: ovsdb.Row{"name": "sibling-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "sibling-ovn"}}}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "siblingPort", Row: ovsdb.Row{"name": "sibling-host", "interfaces": nicPortSet("siblingIface")}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Bridge", UUIDName: "bridge", Row: ovsdb.Row{"name": "br-original", "ports": nicPortSet("siblingPort")}},
				ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Open_vSwitch", Where: nicPortUUIDWhere(o.BackendID()), Mutations: []ovsdb.Mutation{{Column: "bridges", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSet("bridge")}}})
			if mode == "still-bound" {
				_, err := o.CaptureNICPortAbsence(context.Background(), "br-original", "sibling-ovn")
				require.ErrorContains(t, err, "still associated")
				return
			}

			plan, err := o.CaptureNICPortAbsence(context.Background(), "br-original", "original-ovn")
			require.NoError(t, err)
			require.True(t, plan.Absent)
			require.Empty(t, plan.PortUUID)
			wire, err := json.Marshal(plan)
			require.NoError(t, err)
			var stored NICPortCleanup
			require.NoError(t, json.Unmarshal(wire, &stored))
			require.Equal(t, plan, stored)
			switch mode {
			case "bound-after-capture":
				// A non-root Interface row only persists while a rooted Port references it.
				nicOVSBackendExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "lateIface", Row: ovsdb.Row{"name": "late-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "original-ovn"}}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "latePort", Row: ovsdb.Row{"name": "late-host", "interfaces": nicPortSet("lateIface")}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Bridge", Where: nicPortUUIDWhere(stored.BridgeUUID), Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSet("latePort")}}})
			case "bridge-unrooted":
				nicOVSBackendExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Open_vSwitch", Where: nicPortUUIDWhere(o.BackendID()), Mutations: []ovsdb.Mutation{{Column: "bridges", Mutator: ovsdb.MutateOperationDelete, Value: nicPortSet(stored.BridgeUUID)}}})
			case "wrong-root":
				stored.RootUUID = stored.BridgeUUID
			case "malformed":
				for _, broken := range []func(*NICPortCleanup){
					func(p *NICPortCleanup) { p.InterfaceName = "original-host" },
					func(p *NICPortCleanup) { p.PortUUID = p.BridgeUUID },
					func(p *NICPortCleanup) { p.OVNPortName = "" },
					func(p *NICPortCleanup) { p.BridgeUUID = "" },
				} {
					changed := stored
					broken(&changed)
					require.Error(t, changed.Validate())
					require.Error(t, o.ApplyNICPortCleanup(context.Background(), changed))
				}

				return
			}

			before := nicOVSBackendRows(t, raw)
			err = o.ApplyNICPortCleanup(context.Background(), stored)
			if mode == "absent-replay" {
				require.NoError(t, err)
				require.NoError(t, o.ApplyNICPortCleanup(context.Background(), stored))
			} else {
				require.Error(t, err)
			}

			require.Equal(t, before, nicOVSBackendRows(t, raw))
		})
	}
}
