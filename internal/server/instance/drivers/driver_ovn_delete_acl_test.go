package drivers

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/shared/api"
)

func deletedACLFixture(t *testing.T) *committedACLFixture {
	t.Helper()
	f := newCommittedACLFixture(t)
	f.d.id = int(f.instanceID)
	f.d.name = "acl-collection"
	require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.d.localConfig, err = cluster.GetInstanceConfig(ctx, tx.Tx(), int(f.instanceID))
		return err
	}))
	require.NoError(t, f.release(), "the ordinary Delete boundary carries no original NIC update lease")
	return f
}

func deleteACLInstanceExists(t *testing.T, f *committedACLFixture) bool {
	t.Helper()
	var found bool
	require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM instances WHERE id=?)`, f.instanceID).Scan(&found)
	}))
	return found
}

func deleteACLNewOwner(t *testing.T, f *committedACLFixture) {
	t.Helper()
	require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "new-owner", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		if err != nil {
			return err
		}

		require.NoError(t, cluster.UpdateInstanceConfig(ctx, tx.Tx(), id, map[string]string{"volatile.uuid": uuid.NewString()}))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "acl-collection", "security.acls": "original"}})
		if err != nil {
			return err
		}

		return cluster.UpdateInstanceDevices(ctx, tx.Tx(), id, devices)
	}))
}

func deleteACLSibling(t *testing.T, f *committedACLFixture) string {
	t.Helper()
	var id int64
	require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, "default", "sibling", "", db.NetworkTypeOVN, map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500", "security.acls": "original"})
		if err != nil {
			return err
		}

		return tx.NetworkCreated("default", "sibling")
	}))
	sw := fmt.Sprintf("incus-net%d-ls-int", id)
	lr := fmt.Sprintf("incus-net%d-lr", id)
	lrp := lr + "-lrp-int"
	retirementExec(t, f.raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Port", UUIDName: "slrp", Row: ovsdb.Row{"name": lrp, "mac": "00:11:22:33:44:66", "networks": ovsdb.OvsSet{GoSet: []any{"198.51.100.1/24"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": lr, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "slrp"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "srouter", Row: ovsdb.Row{"name": sw + "-lsp-router", "type": "router", "external_ids": retirementStringMap(map[string]string{"incus_switch": sw}), "options": retirementStringMap(map[string]string{"router-port": lrp})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "srouter"}}}}})
	require.Eventually(t, func() bool {
		_, err := f.nb.GetLogicalSwitchPortUUID(context.Background(), networkOVN.OVNSwitchPort(sw+"-lsp-router"))
		return err == nil
	}, 3*time.Second, 10*time.Millisecond)
	group := acl.OVNACLNetworkPortGroupName(f.aclID, id)
	require.NoError(t, f.nb.CreatePortGroup(context.Background(), 1, group, acl.OVNACLDirectionalPortGroups(f.aclID).PortGroups(), networkOVN.OVNSwitch(sw), networkOVN.OVNSwitchPort(sw+"-lsp-router")))
	return string(group)
}

func TestDeletedNICACLCollectionNative(t *testing.T) {
	for _, outcome := range []string{"legacy-order", "committed", "legacy-empty-uuid", "delete-failure", "commit-failure", "stale-instance-id", "stale-instance-uuid", "foreign-failure-retry", "new-db-consumer", "new-physical-consumer", "replacement-network", "sibling-owner"} {
		t.Run(outcome, func(t *testing.T) {
			f := deletedACLFixture(t)
			before := f.contents(t)
			if outcome == "legacy-empty-uuid" {
				require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					return cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instanceID, nil)
				}))
				f.d.localConfig = nil
			}

			if outcome == "legacy-order" {
				require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.DeleteInstance(ctx, f.d.project.Name, f.d.name)
				}))
				require.False(t, deleteACLInstanceExists(t, f))
				require.Equal(t, before, f.contents(t), "the original successful DB delete leaves seven Incus groups plus foreign")
				require.Len(t, f.contents(t)["Port_Group"], 8)
				return
			}

			if outcome == "delete-failure" {
				require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, `CREATE TRIGGER owned_delete_failure BEFORE DELETE ON instances BEGIN SELECT RAISE(ABORT, 'owned fixture delete failure'); END`)
					return err
				}))
			}

			if outcome == "commit-failure" {
				require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, `CREATE TABLE owned_delete_commit_guard (instance_id INTEGER REFERENCES instances(id) DEFERRABLE INITIALLY DEFERRED)`)
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, `INSERT INTO owned_delete_commit_guard (instance_id) VALUES (?)`, f.instanceID)
					return err
				}))
			}

			if outcome == "stale-instance-id" {
				f.d.id++
			}

			if outcome == "stale-instance-uuid" {
				f.d.localConfig["volatile.uuid"] = uuid.NewString()
			}

			collections, err := f.d.deleteInstanceRecord()
			switch outcome {
			case "delete-failure", "commit-failure", "stale-instance-id", "stale-instance-uuid":
				require.Error(t, err)
				require.Nil(t, collections)
				require.True(t, deleteACLInstanceExists(t, f))
				require.Equal(t, before, f.contents(t), "failed removal/commit or stale source identity cannot collect backend groups")
				return
			}

			require.NoError(t, err)
			require.Len(t, collections, 1)
			require.Equal(t, f.networkID, collections[0].networkID)
			require.Equal(t, int64(1), collections[0].projectID)
			require.False(t, deleteACLInstanceExists(t, f))
			require.Equal(t, before, f.contents(t), "commit alone does not silently mutate physical resources")
			group := string(acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID))
			switch outcome {
			case "foreign-failure-retry":
				row := retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}})[0].Rows[0]
				owned := row["external_ids"]
				retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": retirementStringMap(map[string]string{"incus_project_id": "2"})}})
				want := f.contents(t)
				err = f.d.collectDeletedInstanceOVNACLs(collections)
				require.ErrorContains(t, err, "Instance deletion committed")
				require.ErrorContains(t, err, "retry with a normal network update")
				require.Equal(t, want, f.contents(t))
				require.False(t, deleteACLInstanceExists(t, f))
				n, err := network.LoadByName(f.d.state, "default", "acl-collection")
				require.NoError(t, err)
				require.Error(t, n.Update(api.NetworkPut{Description: n.Description(), Config: maps.Clone(n.Config())}, "", request.ClientTypeNormal))
				require.Equal(t, want, f.contents(t))
				retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": owned}})
				require.NoError(t, n.Update(api.NetworkPut{Description: n.Description(), Config: maps.Clone(n.Config())}, "", request.ClientTypeNormal))
			case "new-db-consumer":
				deleteACLNewOwner(t, f)
				require.NoError(t, f.d.collectDeletedInstanceOVNACLs(collections))
				require.Equal(t, before, f.contents(t))
				return
			case "new-physical-consumer":
				sw := fmt.Sprintf("incus-net%d-ls-int", f.networkID)
				retirementExec(t, f.raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "new", Row: ovsdb.Row{"name": "new-consumer", "external_ids": retirementStringMap(map[string]string{"incus_switch": sw})}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: sw}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new"}}}}}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new"}}}}}})
				want := f.contents(t)
				require.ErrorContains(t, f.d.collectDeletedInstanceOVNACLs(collections), "Instance deletion committed")
				require.Equal(t, want, f.contents(t))
				return
			case "replacement-network":
				require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, tx.DeleteNetwork(ctx, "default", "acl-collection"))
					_, err := tx.CreateNetwork(ctx, "default", "acl-collection", "", db.NetworkTypeOVN, nil)
					return err
				}))
				require.ErrorContains(t, f.d.collectDeletedInstanceOVNACLs(collections), "Instance deletion committed")
				require.Equal(t, before, f.contents(t))
				return
			case "sibling-owner":
				sibling := deleteACLSibling(t, f)
				want := f.contents(t)
				require.NoError(t, f.d.collectDeletedInstanceOVNACLs(collections))
				after := f.contents(t)
				for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port"} {
					require.Equal(t, want[table], after[table])
				}

				found := false
				for _, row := range after["Port_Group"] {
					require.NotEqual(t, group, row["name"])
					if row["name"] == sibling {
						found = true
						require.Contains(t, want["Port_Group"], row)
					}
				}

				require.True(t, found)
				require.Len(t, after["Port_Group"], len(want["Port_Group"])-1, "only deleting network constructor group retires; shared directional groups remain")
				return
			default:
				require.NoError(t, f.d.collectDeletedInstanceOVNACLs(collections))
			}

			after := f.contents(t)
			for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port"} {
				require.Equal(t, before[table], after[table])
			}

			require.Len(t, after["Port_Group"], 2, "baseline and foreign group preserved; seven Incus groups become one")
			require.Empty(t, after["ACL"])
			require.False(t, deleteACLInstanceExists(t, f))
		})
	}
}

func TestDeleteRecordWithoutACLCollection(t *testing.T) {
	for _, outcome := range []string{"no-managed-nic", "no-acl-catalog", "snapshot"} {
		t.Run(outcome, func(t *testing.T) {
			f := deletedACLFixture(t)
			before := f.contents(t)
			require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				switch outcome {
				case "no-managed-nic":
					return cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instanceID, nil)
				case "no-acl-catalog":
					return cluster.DeleteNetworkACL(ctx, tx.Tx(), int(f.aclID))
				default:
					_, err := cluster.CreateInstanceSnapshot(ctx, tx.Tx(), cluster.InstanceSnapshot{Project: "default", Instance: "acl-collection", Name: "snap", CreationDate: time.Now()})
					return err
				}
			}))
			if outcome == "snapshot" {
				f.d.name = "acl-collection/snap"
				f.d.isSnapshot = true
			}

			f.d.state.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) {
				t.Fatal("no selected collection may contact the backend")
				return nil, nil, nil
			}

			collections, err := f.d.deleteInstanceRecord()
			require.NoError(t, err)
			require.Empty(t, collections)
			require.NoError(t, f.d.collectDeletedInstanceOVNACLs(collections))
			require.Equal(t, before, f.contents(t))
			require.Equal(t, outcome == "snapshot", deleteACLInstanceExists(t, f), "snapshot deletion keeps its original parent instance")
		})
	}
}
