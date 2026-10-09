package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	ovnModel "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
)

// The Unix-only NB helper follows the existing driver retirement fixture and positive teardown.
func projectCleanupMap(input map[string]string) ovsdb.OvsMap {
	wire := ovsdb.OvsMap{GoMap: map[any]any{}}
	for k, v := range input {
		wire.GoMap[k] = v
	}

	return wire
}

func projectCleanupNB(t *testing.T) (*networkOVN.NB, ovsdbClient.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rt-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schema, err := json.Marshal(ovnModel.Schema())
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
	m, err := ovnModel.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	projectCleanupExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": projectCleanupMap(map[string]string{})}})
	nb, err := networkOVN.NewNB("unix:"+socket, "", "", "", "reference-fixture")
	require.NoError(t, err)

	t.Cleanup(nb.Close)
	t.Logf("owned Unix-only NB fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw
}

func projectCleanupExec(t *testing.T, c ovsdbClient.Client, ops ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(result, ops)
	require.NoError(t, err, "results: %#v", result)
	return result
}

type projectCleanupClient struct {
	incus.InstanceServer
	getNetwork    func(string) (*api.Network, string, error)
	updateNetwork func(string, api.NetworkPut, string) error
	getACL        func(string) (*api.NetworkACL, string, error)
	updateACL     func(string, api.NetworkACLPut, string) error
}

func (c projectCleanupClient) GetNetwork(name string) (*api.Network, string, error) {
	return c.getNetwork(name)
}

func (c projectCleanupClient) UpdateNetwork(name string, put api.NetworkPut, etag string) error {
	return c.updateNetwork(name, put, etag)
}

func (c projectCleanupClient) GetNetworkACL(name string) (*api.NetworkACL, string, error) {
	return c.getACL(name)
}

func (c projectCleanupClient) UpdateNetworkACL(name string, put api.NetworkACLPut, etag string) error {
	return c.updateACL(name, put, etag)
}

func TestForcedProjectACLOrdering(t *testing.T) {
	for _, outcome := range []string{"success", "collection-failure", "acl-failure", "no-acls"} {
		t.Run(outcome, func(t *testing.T) {
			var calls []string
			failure := errors.New("owned failure")
			client := projectCleanupClient{
				getACL: func(name string) (*api.NetworkACL, string, error) {
					calls = append(calls, "get-acl:"+name)
					info := &api.NetworkACL{NetworkACLPost: api.NetworkACLPost{Name: name}, NetworkACLPut: api.NetworkACLPut{Description: "kept", Config: api.ConfigMap{"user.example": "kept"}}}
					if name != "empty" {
						info.Ingress = []api.NetworkACLRule{{Source: "@own/peer"}}
					}

					return info, "acl-etag", nil
				},
				updateACL: func(name string, put api.NetworkACLPut, _ string) error {
					calls = append(calls, "update-acl:"+name)
					require.Empty(t, put.Ingress)
					require.Empty(t, put.Egress)
					require.Equal(t, "kept", put.Description)
					require.Equal(t, api.ConfigMap{"user.example": "kept"}, put.Config)
					if outcome == "acl-failure" {
						return failure
					}

					return nil
				},
			}

			acls := []string{"own-acl", "empty"}
			if outcome == "no-acls" {
				acls = nil
			}

			err := projectDeleteClearNetworkACLRules(client, acls, func() error {
				calls = append(calls, "collect-exact-network-ids")
				if outcome == "collection-failure" {
					return failure
				}

				return nil
			})
			switch outcome {
			case "success":
				require.NoError(t, err)
				require.Equal(t, []string{"collect-exact-network-ids", "get-acl:own-acl", "update-acl:own-acl", "get-acl:empty"}, calls)
			case "collection-failure":
				require.ErrorIs(t, err, failure)
				require.Equal(t, []string{"collect-exact-network-ids"}, calls)
			case "acl-failure":
				require.ErrorIs(t, err, failure)
				require.Equal(t, []string{"collect-exact-network-ids", "get-acl:own-acl", "update-acl:own-acl"}, calls)
			case "no-acls":
				require.NoError(t, err)
				require.Empty(t, calls)
			}
		})
	}
}

type projectCleanupFixture struct {
	t          *testing.T
	s          *state.State
	nb         *networkOVN.NB
	raw        ovsdbClient.Client
	networkID  int64
	aclID      int64
	client     projectCleanupClient
	aclUpdates int
}

func newProjectCleanupFixture(t *testing.T) *projectCleanupFixture {
	t.Helper()
	ctx := context.Background()
	nb, raw := projectCleanupNB(t)
	c := sharedReferenceTestCluster(t)
	f := &projectCleanupFixture{t: t, nb: nb, raw: raw}
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.networkID, err = tx.CreateNetwork(ctx, "default", "project-cleanup", "unchanged", db.NetworkTypeOVN, map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500"})
		if err != nil {
			return err
		}

		require.NoError(t, tx.NetworkCreated("default", "project-cleanup"))
		f.aclID, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "peer-acl", Ingress: []api.NetworkACLRule{{Action: "allow", State: "enabled", Source: "@project-cleanup/peer"}}})
		if err != nil {
			return err
		}

		require.NoError(t, tx.BeginOVNReferenceActivation(ctx))
		return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
	}))
	f.s = &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OS: &sys.OS{}, OVN: func() (*networkOVN.NB, *networkOVN.SB, error) { return nb, &networkOVN.SB{}, nil }}
	sw := fmt.Sprintf("incus-net%d-ls-int", f.networkID)
	lr := fmt.Sprintf("incus-net%d-lr", f.networkID)
	lrp := lr + "-lrp-int"
	projectCleanupExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Port", UUIDName: "lrp", Row: ovsdb.Row{"name": lrp, "mac": "00:11:22:33:44:55", "networks": ovsdb.OvsSet{GoSet: []any{"192.0.2.1/24"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": lr, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "lrp"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "router", Row: ovsdb.Row{"name": sw + "-lsp-router", "type": "router", "external_ids": projectCleanupMap(map[string]string{"incus_switch": sw}), "options": projectCleanupMap(map[string]string{"router-port": lrp})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "router"}}}}})
	require.Eventually(t, func() bool { return nb.CreatePortGroup(ctx, 1, "foreign-preserved", nil, "") == nil }, 3*time.Second, 10*time.Millisecond)
	directional := acl.OVNACLDirectionalPortGroups(f.aclID).PortGroups()
	for _, group := range directional {
		require.NoError(t, nb.CreatePortGroup(ctx, 1, group, nil, ""))
	}

	group := acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID)
	require.NoError(t, nb.CreatePortGroup(ctx, 1, group, directional, networkOVN.OVNSwitch(sw), networkOVN.OVNSwitchPort(sw+"-lsp-router")))
	var rules []networkOVN.OVNACLRule
	for _, subject := range directional {
		rules = append(rules, networkOVN.OVNACLRule{Direction: "to-lport", Action: "drop", Match: "inport == @" + string(subject), Priority: 123})
	}

	require.NoError(t, nb.UpdatePortGroupACLRules(ctx, group, nil, rules...))
	require.NoError(t, nb.CreatePortGroup(ctx, 1, acl.OVNIntSwitchPortGroupName(f.networkID), nil, ""))
	f.client = projectCleanupClient{
		getNetwork: func(name string) (*api.Network, string, error) {
			n, err := network.LoadByName(f.s, "default", name)
			if err != nil {
				return nil, "", err
			}

			return &api.Network{Name: name, Type: n.Type(), Status: n.Status(), NetworkPut: api.NetworkPut{Description: n.Description(), Config: maps.Clone(n.Config())}}, "current-native", nil
		},
		updateNetwork: func(name string, put api.NetworkPut, etag string) error {
			require.Equal(t, "current-native", etag)
			n, err := network.LoadByName(f.s, "default", name)
			if err != nil {
				return err
			}

			require.Equal(t, api.ConfigMap(n.Config()), put.Config)
			require.Equal(t, n.Description(), put.Description)
			return n.Update(put, "", request.ClientTypeNormal)
		},
		getACL: func(name string) (*api.NetworkACL, string, error) {
			a, err := acl.LoadByName(f.s, "default", name)
			if err != nil {
				return nil, "", err
			}

			return a.Info(), "native-acl", nil
		},
		updateACL: func(name string, put api.NetworkACLPut, _ string) (err error) {
			f.aclUpdates++
			release, before, err := networkReserveSharedOVN(f.s, "default", "acl-config", request.ClientTypeNormal)
			if err != nil {
				return err
			}

			defer func() { err = errors.Join(err, release()) }()
			a, err := acl.LoadByName(f.s, "default", name)
			if err != nil {
				return err
			}

			return a.Update(&put, request.ClientTypeNormal, before)
		},
	}

	return f
}

func (f *projectCleanupFixture) collect() error {
	return network.OVNCollectNetworkACLGroups(f.s, "default", "project-cleanup", 1, f.networkID)
}

func (f *projectCleanupFixture) rows() map[string][]ovsdb.Row {
	f.t.Helper()
	rows := map[string][]ovsdb.Row{}
	for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port", "Port_Group", "ACL"} {
		rows[table] = projectCleanupExec(f.t, f.raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}})[0].Rows
	}

	return rows
}

func (f *projectCleanupFixture) addCurrentNIC() {
	f.t.Helper()
	require.NoError(f.t, f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var member string
		require.NoError(f.t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&member))
		id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "new-owner", Node: member, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		if err != nil {
			return err
		}

		require.NoError(f.t, cluster.UpdateInstanceConfig(ctx, tx.Tx(), id, map[string]string{"volatile.uuid": uuid.NewString()}))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "project-cleanup", "security.acls": "peer-acl"}})
		if err != nil {
			return err
		}

		return cluster.UpdateInstanceDevices(ctx, tx.Tx(), id, devices)
	}))
}

func TestForcedProjectACLCollectionNative(t *testing.T) {
	for _, outcome := range []string{"unused", "foreign-failure-retry", "new-physical-consumer", "replacement-network", "replacement-project", "current-nic-consumer", "current-network-consumer"} {
		t.Run(outcome, func(t *testing.T) {
			f := newProjectCleanupFixture(t)
			before := f.rows()
			old, _, err := f.client.GetNetworkACL("peer-acl")
			require.NoError(t, err)
			clearedACL := old.Writable()
			clearedACL.Ingress, clearedACL.Egress = nil, nil
			err = f.client.UpdateNetworkACL("peer-acl", clearedACL, "")
			require.ErrorIs(t, err, networkOVN.ErrPhysicalReference, "the original clear-before-network-cleanup order must refuse the retained physical group")
			t.Logf("original ACL-clear refusal with one constructor member and no NIC owner: %v", err)
			require.Equal(t, before, f.rows())
			current, _, err := f.client.GetNetworkACL("peer-acl")
			require.NoError(t, err)
			require.Equal(t, old, current, "refused clear rolls back the uncommitted ACL definition")
			f.aclUpdates = 0
			group := string(acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID))
			switch outcome {
			case "foreign-failure-retry":
				row := projectCleanupExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}})[0].Rows[0]
				owned := row["external_ids"]
				projectCleanupExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": projectCleanupMap(map[string]string{"incus_project_id": "2"})}})
				want := f.rows()
				require.Error(t, projectDeleteClearNetworkACLRules(f.client, []string{"peer-acl"}, f.collect))
				require.Zero(t, f.aclUpdates)
				require.Equal(t, want, f.rows())
				current, _, err = f.client.GetNetworkACL("peer-acl")
				require.NoError(t, err)
				require.Equal(t, old, current)
				projectCleanupExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": owned}})
			case "new-physical-consumer":
				sw := fmt.Sprintf("incus-net%d-ls-int", f.networkID)
				projectCleanupExec(t, f.raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "new", Row: ovsdb.Row{"name": "new-consumer", "external_ids": projectCleanupMap(map[string]string{"incus_switch": sw})}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: sw}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new"}}}}}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new"}}}}}})
				want := f.rows()
				require.Error(t, projectDeleteClearNetworkACLRules(f.client, []string{"peer-acl"}, f.collect))
				require.Zero(t, f.aclUpdates)
				require.Equal(t, want, f.rows())
				return
			case "replacement-network", "replacement-project":
				require.NoError(t, f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					if outcome == "replacement-network" {
						require.NoError(t, tx.DeleteNetwork(ctx, "default", "project-cleanup"))
						_, err := tx.CreateNetwork(ctx, "default", "project-cleanup", "", db.NetworkTypeOVN, nil)
						return err
					}

					require.NoError(t, cluster.RenameProject(ctx, tx.Tx(), "default", "original-project"))
					_, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "default"})
					return err
				}))
				require.Error(t, projectDeleteClearNetworkACLRules(f.client, []string{"peer-acl"}, f.collect))
				require.Zero(t, f.aclUpdates)
				require.Equal(t, before, f.rows(), "same-name replacement cannot authorize the captured numeric network/project collection")
				return
			case "current-nic-consumer", "current-network-consumer":
				if outcome == "current-nic-consumer" {
					f.addCurrentNIC()
				} else {
					require.NoError(t, f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
						return tx.UpdateNetwork(ctx, "default", "project-cleanup", "unchanged", map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500", "security.acls": "peer-acl"})
					}))
				}

				n, etag, err := f.client.GetNetwork("project-cleanup")
				require.NoError(t, err)
				require.NoError(t, f.client.UpdateNetwork(n.Name, n.Writable(), etag))
				require.Equal(t, before, f.rows(), "current owners protect all ACL groups during the collector step")
				return
			}

			require.NoError(t, projectDeleteClearNetworkACLRules(f.client, []string{"peer-acl"}, f.collect))
			require.Equal(t, 1, f.aclUpdates)
			after := f.rows()
			for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port"} {
				require.Equal(t, before[table], after[table], table+" exact native infrastructure is preserved")
			}

			require.Len(t, after["Port_Group"], 2, "only baseline and foreign groups remain; 6 unused ACL groups retired")
			require.Empty(t, after["ACL"])
			a, err := acl.LoadByName(f.s, "default", "peer-acl")
			require.NoError(t, err)
			require.Empty(t, a.Info().Ingress)
			require.NoError(t, a.Delete(), "ordinary ACL deletion now succeeds with physical guards unchanged")
			require.NoError(t, f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var count int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM networks_ovn_operations`).Scan(&count))
				require.Zero(t, count, "normal network and shared ACL reservations are released")
				return nil
			}))
		})
	}
}
