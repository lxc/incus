package ovn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	model "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

func referenceTestNB(t *testing.T, relayMode ...bool) (*NB, ovsdbClient.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pr-")
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("failed owned fixture retained: %s", dir)
			return
		}

		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schemaModel := model.Schema()
	if len(relayMode) > 0 {
		schemaModel.Tables["Logical_Switch"].Columns["future_ref_data"] = schemaModel.Tables["Logical_Switch"].Columns["external_ids"]
		schemaModel.Tables["Address_Set"].Columns["future_prefix_data"] = schemaModel.Tables["Address_Set"].Columns["external_ids"]
		schemaModel.Tables["Logical_Switch_Port"].Columns["future_prefix_data"] = schemaModel.Tables["Logical_Switch_Port"].Columns["external_ids"]
	}

	schema, err := json.Marshal(schemaModel)
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
	m, err := model.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	root := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})[0].UUID
	endpoint := "unix:" + socket
	if len(relayMode) > 0 && relayMode[0] {
		relaySocket := filepath.Join(dir, "relay-socket")
		relay := exec.Command("/usr/sbin/ovsdb-server", "relay:OVN_Northbound:"+endpoint, "--remote=punix:"+relaySocket, "--unixctl="+filepath.Join(dir, "relay-control"), "--pidfile="+filepath.Join(dir, "relay-pid"), "--log-file="+filepath.Join(dir, "relay-log"), "--no-chdir")
		relay.Stdout, relay.Stderr = logFile, logFile
		require.NoError(t, relay.Start())
		t.Cleanup(func() {
			require.NoError(t, relay.Process.Signal(syscall.SIGTERM))
			done := make(chan error, 1)
			go func() { done <- relay.Wait() }()
			select {
			case err := <-done:
				require.Error(t, err, logOutput())
				require.Equal(t, syscall.SIGTERM, relay.ProcessState.Sys().(syscall.WaitStatus).Signal())
				t.Logf("owned Unix-only relay PID=%d reaped after requested SIGTERM", relay.Process.Pid)
			case <-time.After(5 * time.Second):
				_ = relay.Process.Kill()
				<-done
				t.Error("owned relay required forced teardown")
			}
		})
		endpoint = "unix:" + relaySocket
		referenceWaitRelay(t, endpoint, root, filepath.Join(dir, "readiness-log"), logOutput)
		t.Logf("owned relay PID=%d endpoint=%s upstream=%s", relay.Process.Pid, endpoint, socket)
	}

	nb, err := NewNB(endpoint, "", "", "", "reference-fixture")
	require.NoError(t, err)
	t.Cleanup(nb.client.Close)
	t.Logf("owned Unix-only NB fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw
}

// referenceWaitRelay reads the owned relay's schema and upstream root before creating NB.
// It retries only fixture startup probes; predicate and content assertions remain single attempts.
func referenceWaitRelay(t *testing.T, endpoint string, root ovsdb.UUID, logPath string, logOutput func() string) {
	t.Helper()
	m, err := model.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	attempts := []map[string]any{}
	ready := false
	defer func() {
		data, err := json.MarshalIndent(map[string]any{"endpoint": endpoint, "expected_root": root.GoUUID, "ready": ready, "attempts": attempts}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(logPath, data, 0o600))
		t.Logf("Owned relay readiness observations: %s", data)
	}()

	var lastErr error
	for {
		probe, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint(endpoint), ovsdbClient.WithLogger(&discard))
		require.NoError(t, err)
		attemptCtx, attemptCancel := context.WithTimeout(ctx, time.Second)
		lastErr = probe.Connect(attemptCtx)
		observedSchema := ""
		observedRoot := ""
		unexpected := false
		if lastErr == nil {
			observedSchema = probe.Schema().Name
			if observedSchema != model.Schema().Name {
				lastErr = fmt.Errorf("Owned relay returned unexpected schema %q", observedSchema)
				unexpected = true
			} else {
				op := ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "NB_Global", Where: []ovsdb.Condition{}, Columns: []string{"_uuid"}}
				var results []ovsdb.OperationResult
				results, lastErr = probe.Transact(attemptCtx, op)
				if lastErr == nil {
					_, lastErr = ovsdb.CheckOperationResults(results, []ovsdb.Operation{op})
				}

				if lastErr == nil {
					if len(results) != 1 || len(results[0].Rows) != 1 {
						lastErr = fmt.Errorf("Owned relay root read returned %d results without exactly one root", len(results))
						unexpected = len(results) != 1 || len(results[0].Rows) > 1
					} else {
						id, ok := results[0].Rows[0]["_uuid"].(ovsdb.UUID)
						observedRoot = id.GoUUID
						if !ok || id != root {
							lastErr = fmt.Errorf("Owned relay root %q differs from upstream root %q", observedRoot, root.GoUUID)
							unexpected = true
						}
					}
				}
			}
		}

		probe.Close()
		attemptCancel()
		record := map[string]any{"elapsed_ms": time.Since(started).Milliseconds(), "schema": observedSchema, "root": observedRoot}
		if lastErr != nil {
			record["error"] = lastErr.Error()
		}

		attempts = append(attempts, record)
		if lastErr == nil {
			ready = true
			t.Logf("owned relay protocol ready endpoint=%s root=%s attempts=%d", endpoint, root.GoUUID, len(attempts))
			return
		}

		if unexpected {
			require.NoError(t, lastErr, "Owned relay identity mismatch; process logs: %s", logOutput())
		}

		select {
		case <-ctx.Done():
			require.NoError(t, errors.Join(ctx.Err(), lastErr), "Owned relay readiness deadline after %d attempts; process logs: %s", len(attempts), logOutput())
		case <-ticker.C:
		}
	}
}

func referenceExec(t *testing.T, c ovsdbClient.Client, ops ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(result, ops)
	require.NoError(t, err, "results: %#v", result)
	return result
}

func referencePort(t *testing.T, c ovsdbClient.Client) (string, string) {
	t.Helper()
	result := referenceExec(t, c,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "port", Row: ovsdb.Row{"name": "incus-net17-instance-port", "enabled": ovsdb.OvsSet{GoSet: []any{false}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusLocation: "source"}), "addresses": ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:55 192.0.2.9"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net17-ls-int", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}}})
	return result[0].UUID.GoUUID, result[1].UUID.GoUUID
}

func TestPhysicalLegacyReferencesRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	port, _ := referencePort(t, raw)
	ctx := context.Background()
	require.ErrorIs(t, nb.CheckNetworkPhysicalUnused(ctx, 17, "incus-net17-lr-lrp-int"), ErrPhysicalReference)
	pg := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_acl29_all", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "1"})}})[0].UUID
	require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 1, 29), ErrPhysicalReference)
	want := referenceContents(t, raw)
	require.NoError(t, nb.DeletePortGroup(ctx, "incus_acl29_all"))
	referenceSameContents(t, want, referenceContents(t, raw))
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{}}}})
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "rule", Row: ovsdb.Row{"match": "ip4.src == $incus_set31_ip4 && inport == @incus_acl29_all", "action": "drop", "direction": "to-lport", "priority": 1}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign", "acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "rule"}}}}})
	require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 1, 29), ErrPhysicalReference)
	require.ErrorIs(t, nb.CheckAddressSetPhysicalUnused(ctx, 31), ErrPhysicalReference)
	want = referenceContents(t, raw)
	require.NoError(t, nb.DeletePortGroup(ctx, "incus_acl29_all"))
	referenceSameContents(t, want, referenceContents(t, raw))
	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}})
	require.Len(t, rows[0].Rows, 1)
	require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 2, 29), ErrPhysicalReference)
}

func TestPhysicalReferenceEffectBoundaryRaceRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	port, _ := referencePort(t, raw)
	ctx := context.Background()
	pg := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_acl29_all", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "1"})}})[0].UUID
	snapshot, err := nb.physicalReferenceSnapshot(ctx)
	require.NoError(t, err)
	require.NoError(t, snapshot.groupUnused("incus_acl29_all", 1))
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}}})
	ops := snapshot.waits()
	ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}})
	_, err = nb.nicCleanupTransact(ctx, ops...)
	require.ErrorContains(t, err, "timed out")
	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}})
	require.Len(t, rows[0].Rows, 1)
}

func TestPhysicalReferencePortableContentRealBackend(t *testing.T) {
	for _, relay := range []bool{false, true} {
		transport := "native"
		if relay {
			transport = "relay"
		}

		for _, change := range []string{"unchanged", "reference", "producer", "insert", "remove", "future-column"} {
			t.Run(transport+"/"+change, func(t *testing.T) {
				nb, raw := referenceTestNB(t, relay)
				port, sw := referencePort(t, raw)
				pg := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "control-group"}})[0].UUID
				extra := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "membership-control"}})[0].UUID
				ctx := context.Background()
				selectVersion := ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: sw}}}, Columns: []string{"_uuid", "_version"}}
				// A relay applies upstream updates asynchronously; wait until it serves the inserted row.
				var clientVersion []ovsdb.Row
				require.Eventually(t, func() bool {
					clientVersion = referenceExec(t, nb.client, selectVersion)[0].Rows
					return len(clientVersion) == 1
				}, 5*time.Second, 10*time.Millisecond)
				nativeVersion := referenceExec(t, raw, selectVersion)[0].Rows
				require.Len(t, clientVersion, 1)
				require.Len(t, nativeVersion, 1)
				require.Equal(t, nativeVersion[0]["_uuid"], clientVersion[0]["_uuid"])
				if relay {
					require.NotEqual(t, nativeVersion[0]["_version"], clientVersion[0]["_version"])
				} else {
					require.Equal(t, nativeVersion[0]["_version"], clientVersion[0]["_version"])
				}

				snapshot, err := nb.physicalReferenceSnapshot(ctx)
				require.NoError(t, err)
				switch change {
				case "reference":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}}})
				case "producer":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: port}}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusLocation: "different-source"})}})
				case "insert":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "new-membership"}})
				case "remove":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: extra}}})
				case "future-column":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: sw}}}, Row: ovsdb.Row{"future_ref_data": nicCleanupStringMapWire(map[string]string{"state": "changed"})}})
				}

				before := referenceContents(t, raw)
				ops := append(snapshot.waits(), ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: sw}}}, Row: ovsdb.Row{"other_config": nicCleanupStringMapWire(map[string]string{"guarded-effect": "applied"})}})
				_, err = nb.nicCleanupTransact(ctx, ops...)
				if change == "unchanged" {
					require.NoError(t, err)
					rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: sw}}}, Columns: []string{"other_config"}})[0].Rows
					require.Len(t, rows, 1)
					require.Equal(t, nicCleanupStringMapWire(map[string]string{"guarded-effect": "applied"}), rows[0]["other_config"])
				} else {
					require.ErrorContains(t, err, "timed out")
					referenceSameContents(t, before, referenceContents(t, raw))
				}

				for _, op := range snapshot.waits() {
					if op.Op == ovsdb.OperationWait && op.Table == "Logical_Switch" {
						require.Contains(t, op.Columns, "future_ref_data")
						require.Contains(t, op.Columns, "_uuid")
						require.NotContains(t, op.Columns, "_version")
					}
				}
			})
		}
	}
}

func TestNICConfigPublicationActualOutcomeRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	port, _ := referencePort(t, raw)
	ctx := context.Background()
	input := NICConfigPublication{Generation: uuid.NewString(), NetworkID: 17, ProjectID: 1, InstanceUUID: uuid.NewString(), Device: "eth0", Source: "source", Phase: "add", Input: map[string]string{"hwaddr": "00:11:22:33:44:55"}, ACLIDs: map[string]int64{"original": 29}, MAC: "00:11:22:33:44:55", IPs: []string{"192.0.2.9"}}
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input))
	_, err := nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.Error(t, err)
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: port}}}, Row: ovsdb.Row{"enabled": ovsdb.OvsSet{GoSet: []any{true}}}})
	input.Phase = "start"
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input))
	receipt, err := nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.NoError(t, err)
	require.Equal(t, nb.BackendID(), receipt.RootUUID)
	require.Equal(t, port, receipt.PortUUID)
	input.ACLIDs["original"] = 30
	_, err = nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.Error(t, err)
	input.ACLIDs["original"] = 29
	require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", "incus-net17-instance-port"))
	_, err = nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.Error(t, err)
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input))
	_, err = nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.NoError(t, err)
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: port}}}, Row: ovsdb.Row{"addresses": ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:66 192.0.2.10"}}}})
	_, err = nb.CheckNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", input)
	require.Error(t, err)
}

func TestPhysicalReferenceWholeTablePredicateRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	_, _ = referencePort(t, raw)
	zero := ovsdb.UUID{GoUUID: "00000000-0000-0000-0000-000000000000"}
	inserted := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", UUID: zero.GoUUID, Row: ovsdb.Row{"name": "zero-uuid", "external_ids": nicCleanupStringMapWire(map[string]string{"foreign": "owner"})}})
	require.Equal(t, zero, inserted[0].UUID)
	for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Port_Group", "ACL", "Address_Set", "Logical_Router_Policy", "Logical_Router"} {
		result := referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}},
			ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: nicCleanupStringMapWire(map[string]string{})}}})
		require.ElementsMatch(t, result[0].Rows, result[1].Rows, table)
	}

	require.NoError(t, nb.CheckAddressSetPhysicalUnused(context.Background(), 31))
}

// referenceEffectClient changes the real backend after the snapshot, before the guarded effect.
type referenceEffectClient struct {
	ovsdbClient.Client
	before    func()
	loseReply bool
}

func (c *referenceEffectClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range ops {
		if op.Op == ovsdb.OperationDelete {
			if c.before != nil {
				before := c.before
				c.before = nil
				before()
			}

			result, err := c.Client.Transact(ctx, ops...)
			if c.loseReply && err == nil {
				return nil, context.DeadlineExceeded
			}

			return result, err
		}
	}

	return c.Client.Transact(ctx, ops...)
}

func referenceContents(t *testing.T, raw ovsdbClient.Client) map[string][]ovsdb.Row {
	t.Helper()
	tables := []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Port_Group", "ACL", "Address_Set", "Logical_Router_Policy", "Logical_Router"}
	ops := make([]ovsdb.Operation, 0, len(tables))
	for _, table := range tables {
		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}})
	}

	result := referenceExec(t, raw, ops...)
	contents := map[string][]ovsdb.Row{}
	for i, table := range tables {
		contents[table] = result[i].Rows
	}

	return contents
}

func referenceSameContents(t *testing.T, want, actual map[string][]ovsdb.Row) {
	t.Helper()
	for table, rows := range want {
		require.ElementsMatch(t, rows, actual[table], table)
	}
}

func referenceRemoveUUID(contents map[string][]ovsdb.Row, table string, id ovsdb.UUID) {
	retained := []ovsdb.Row{}
	for _, row := range contents[table] {
		if row["_uuid"] != id {
			retained = append(retained, row)
		}
	}

	contents[table] = retained
}

func TestPhysicalDestructiveCallersEmptyTablesRealBackend(t *testing.T) {
	for _, action := range []string{"group", "groups", "address-set", "internal-switch"} {
		t.Run(action, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			ctx := context.Background()
			initial := referenceContents(t, raw)
			require.Equal(t, ovsdb.UUID{GoUUID: nb.BackendID()}, initial["NB_Global"][0]["_uuid"])
			for table, rows := range initial {
				if table != "NB_Global" {
					require.Empty(t, rows, table)
				}
			}

			table := "Port_Group"
			names := []string{"incus_acl29_all"}
			switch action {
			case "groups":
				names = append(names, "incus_acl29_net17")
			case "address-set":
				table = "Address_Set"
				names = []string{"incus_set31_ip4", "incus_set31_ip6"}
			case "internal-switch":
				table = "Logical_Switch"
				names = []string{"incus-net17-ls-int"}
			}

			ops := []ovsdb.Operation{}
			for _, name := range append(append([]string{}, names...), "foreign", "sibling") {
				project := "1"
				if name == "foreign" {
					project = "2"
				}

				ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: ovsdb.Row{"name": name, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: project})}})
			}

			result := referenceExec(t, raw, ops...)
			want := referenceContents(t, raw)
			for i := range names {
				referenceRemoveUUID(want, table, result[i].UUID)
			}

			if action == "internal-switch" {
				require.Eventually(t, func() bool {
					sw := model.LogicalSwitch{Name: names[0]}
					err := nb.get(ctx, &sw)
					return err == nil && sw.UUID == result[0].UUID.GoUUID
				}, time.Second, time.Millisecond)
			}

			var err error
			switch action {
			case "group":
				err = nb.DeletePortGroup(ctx, OVNPortGroup(names[0]))
			case "groups":
				err = nb.DeletePortGroup(ctx, OVNPortGroup(names[0]), OVNPortGroup(names[1]))
			case "address-set":
				err = nb.DeleteAddressSet(ctx, "incus_set31")
			case "internal-switch":
				err = nb.DeleteLogicalSwitch(ctx, OVNSwitch(names[0]))
			}

			require.NoError(t, err)
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

func TestPhysicalDestructiveCallerRacesRealBackend(t *testing.T) {
	for _, race := range []string{"changed-group", "new-group-reference", "new-address-set-reference", "new-switch-port", "changed-root", "late-changed-router", "lost-effect-reply"} {
		t.Run(race, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			ctx := context.Background()
			port, _ := referencePort(t, raw)
			pg := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_acl29_all", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "1"})}})[0].UUID
			setOps := []ovsdb.Operation{}
			for _, name := range []string{"incus_set31_ip4", "incus_set31_ip6", "foreign", "sibling"} {
				setOps = append(setOps, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": name}})
			}

			referenceExec(t, raw, setOps...)
			if race == "late-changed-router" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": "foreign-router"}})
			}

			want := referenceContents(t, raw)
			wrapped := &referenceEffectClient{Client: nb.client}
			wrapped.before = func() {
				switch race {
				case "changed-group":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: pg}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}}})
				case "new-group-reference", "new-address-set-reference":
					match := "inport == @incus_acl29_all"
					if race == "new-address-set-reference" {
						match = "ip4.src == $incus_set31_ip4"
					}

					referenceExec(t, raw,
						ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "new_reference", Row: ovsdb.Row{"action": "drop", "direction": "to-lport", "match": match, "priority": 1}},
						ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign", "acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new_reference"}}}}})
				case "new-switch-port":
					referenceExec(t, raw,
						ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "new_port", Row: ovsdb.Row{"name": "new-disabled-port", "enabled": ovsdb.OvsSet{GoSet: []any{false}}}},
						ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new_port"}}}}})
				case "late-changed-router":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "foreign-router"}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{"changed": "after-snapshot"})}})
				case "changed-root":
					referenceExec(t, raw,
						ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: nb.BackendID()}}}},
						ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})
				}

				want = referenceContents(t, raw)
			}

			nb.client = wrapped
			var err error
			switch race {
			case "new-address-set-reference":
				err = nb.DeleteAddressSet(ctx, "incus_set31")
			case "new-switch-port":
				// Replace the occupied switch with an unused one before taking the snapshot.
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{}}}})
				err = nb.DeleteLogicalSwitch(ctx, "incus-net17-ls-int")
			default:
				wrapped.loseReply = race == "lost-effect-reply"
				err = nb.DeletePortGroup(ctx, "incus_acl29_all")
			}

			require.Nil(t, wrapped.before, "the real destructive caller must reach the guarded transaction")
			switch race {
			case "lost-effect-reply":
				require.True(t, errors.Is(err, context.DeadlineExceeded))
				referenceRemoveUUID(want, "Port_Group", pg)
			default:
				require.ErrorContains(t, err, "timed out")
			}

			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

func TestPhysicalDestructiveCallerUnknownRootRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	for _, root := range []string{"", "unknown", uuid.NewString()} {
		t.Run(fmt.Sprintf("root-%s", root), func(t *testing.T) {
			want := referenceContents(t, raw)
			original := nb.backendID
			nb.backendID = root
			defer func() { nb.backendID = original }()
			require.Error(t, nb.DeletePortGroup(context.Background(), "incus_acl29_all"))
			require.Error(t, nb.DeleteAddressSet(context.Background(), "incus_set31"))
			require.Error(t, nb.DeleteLogicalSwitch(context.Background(), "incus-net17-ls-int"))
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

// TestPhysicalNetworkUnusedTunnelPorts covers deleting a network with a configured tunnel: only its
// own exact tunnel port is accepted as infrastructure.
func TestPhysicalNetworkUnusedTunnelPorts(t *testing.T) {
	for _, mode := range []string{"configured", "unconfigured", "consumer-shaped"} {
		t.Run(mode, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			row := ovsdb.Row{"name": "tunnel-net17-t1", "addresses": ovsdb.OvsSet{GoSet: []any{"unknown"}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"})}
			if mode == "consumer-shaped" {
				row["enabled"] = ovsdb.OvsSet{GoSet: []any{true}}
			}

			referenceExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "tunnel", Row: row},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net17-ls-int", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "tunnel"}}}}})
			client := nb
			if mode != "unconfigured" {
				client = nb.WithNetworkTunnelPorts("tunnel-net17-t1")
			}

			err := client.CheckNetworkPhysicalUnused(context.Background(), 17, "incus-net17-lr-lrp-int")
			if mode == "configured" {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, ErrPhysicalReference)
		})
	}
}
