package drivers

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type committedACLFixture struct {
	d          *common
	nb         *networkOVN.NB
	raw        ovsdbClient.Client
	instanceID int64
	networkID  int64
	aclID      int64
	groups     []networkOVN.OVNPortGroup
	release    func() error
}

func newCommittedACLFixture(t *testing.T) *committedACLFixture {
	t.Helper()
	ctx := context.Background()
	nb, raw := retirementTestNB(t)
	c := retirementTestCluster(t)
	f := &committedACLFixture{nb: nb, raw: raw}
	nic := deviceConfig.Device{"type": "nic", "network": "acl-collection", "security.acls": "original"}
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.networkID, err = tx.CreateNetwork(ctx, "default", "acl-collection", "", db.NetworkTypeOVN, map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500"})
		if err != nil {
			return err
		}

		require.NoError(t, tx.NetworkCreated("default", "acl-collection"))
		f.aclID, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "original"})
		if err != nil {
			return err
		}

		var member string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&member))
		f.instanceID, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "acl-collection", Node: member, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		if err != nil {
			return err
		}

		require.NoError(t, cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instanceID, map[string]string{"volatile.uuid": uuid.NewString()}))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": nic})
		if err != nil {
			return err
		}

		require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instanceID, devices))
		require.NoError(t, tx.BeginOVNReferenceActivation(ctx))
		return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
	}))
	s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OS: &sys.OS{}, OVN: func() (*networkOVN.NB, *networkOVN.SB, error) { return nb, &networkOVN.SB{}, nil }}
	f.d = &common{state: s, project: api.Project{Name: "default"}, expandedDevices: deviceConfig.Devices{"eth0": nic}}
	var err error
	release, err := f.d.reserveOVNDeviceUpdate(deviceConfig.Devices{"eth0": {"type": "nic", "network": "acl-collection"}})
	require.NoError(t, err)
	var releaseOnce sync.Once
	var releaseErr error
	f.release = func() error {
		releaseOnce.Do(func() { releaseErr = release() })
		return releaseErr
	}

	t.Cleanup(func() { require.NoError(t, f.release()) })
	sw := fmt.Sprintf("incus-net%d-ls-int", f.networkID)
	router := fmt.Sprintf("incus-net%d-lr", f.networkID)
	lrp := router + "-lrp-int"
	retirementExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Port", UUIDName: "lrp", Row: ovsdb.Row{"name": lrp, "mac": "00:11:22:33:44:55", "networks": ovsdb.OvsSet{GoSet: []any{"192.0.2.1/24"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": router, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "lrp"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "router", Row: ovsdb.Row{"name": sw + "-lsp-router", "type": "router", "external_ids": retirementStringMap(map[string]string{"incus_switch": sw}), "options": retirementStringMap(map[string]string{"router-port": lrp})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "router"}}}}})
	require.Eventually(t, func() bool {
		return nb.CreatePortGroup(ctx, 1, "foreign-preserved", nil, "") == nil
	}, 3*time.Second, 10*time.Millisecond)
	f.groups = acl.OVNACLDirectionalPortGroups(f.aclID).PortGroups()
	for _, group := range f.groups {
		require.NoError(t, nb.CreatePortGroup(ctx, 1, group, nil, ""))
	}

	group := acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID)
	require.NoError(t, nb.CreatePortGroup(ctx, 1, group, f.groups, networkOVN.OVNSwitch(sw), networkOVN.OVNSwitchPort(sw+"-lsp-router")))
	rules := []networkOVN.OVNACLRule{}
	for _, subject := range f.groups {
		rules = append(rules, networkOVN.OVNACLRule{Direction: "to-lport", Action: "drop", Match: "inport == @" + string(subject), Priority: 123})
	}

	require.NoError(t, nb.UpdatePortGroupACLRules(ctx, group, nil, rules...))
	f.groups = append(f.groups, group)
	require.NoError(t, nb.CreatePortGroup(ctx, 1, acl.OVNIntSwitchPortGroupName(f.networkID), nil, ""))
	return f
}

func (f *committedACLFixture) commit(t *testing.T) {
	t.Helper()
	require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "acl-collection"}})
		if err != nil {
			return err
		}

		return cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instanceID, devices)
	}))
}

func (f *committedACLFixture) contents(t *testing.T) map[string][]ovsdb.Row {
	t.Helper()
	rows := map[string][]ovsdb.Row{}
	for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port", "Port_Group", "ACL"} {
		rows[table] = retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}})[0].Rows
	}

	return rows
}

func TestCommittedNICACLCollectionNative(t *testing.T) {
	for _, outcome := range []string{"before-commit", "committed", "foreign-failure-retry", "new-db-consumer", "new-physical-consumer", "replacement-network", "no-constructor-groups"} {
		t.Run(outcome, func(t *testing.T) {
			f := newCommittedACLFixture(t)
			before := f.contents(t)
			if outcome == "before-commit" {
				require.NoError(t, f.d.finishOVNDeviceUpdate(false, f.release))
				require.Equal(t, before, f.contents(t))
				return
			}

			f.commit(t)
			switch outcome {
			case "foreign-failure-retry":
				group := string(acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID))
				row := retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}})[0].Rows[0]
				ids := row["external_ids"]
				retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": retirementStringMap(map[string]string{"incus_project_id": "2"})}})
				want := f.contents(t)
				err := f.d.finishOVNDeviceUpdate(true, f.release)
				require.ErrorContains(t, err, "Instance configuration committed")
				require.ErrorContains(t, err, "retry with a normal network update")
				require.Equal(t, want, f.contents(t), "a foreign row must not be adopted or removed")
				n, err := network.LoadByName(f.d.state, "default", "acl-collection")
				require.NoError(t, err)
				require.Error(t, n.Update(api.NetworkPut{Config: maps.Clone(n.Config()), Description: n.Description()}, "", request.ClientTypeNormal), "an unchanged retry also refuses the unresolved foreign resource")
				require.Equal(t, want, f.contents(t))
				retirementExec(t, f.raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: group}}, Row: ovsdb.Row{"external_ids": ids}})
				require.NoError(t, n.Update(api.NetworkPut{Config: maps.Clone(n.Config()), Description: n.Description()}, "", request.ClientTypeNormal), "the actual unchanged network Update must reach cleanup")
			case "new-db-consumer":
				require.NoError(t, f.d.finishOVNDeviceUpdate(true, func() error {
					require.NoError(t, f.release())
					return f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
						return tx.UpdateNetwork(ctx, "default", "acl-collection", "", map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500", "security.acls": "original"})
					})
				}))
				require.Equal(t, before, f.contents(t), "a new current network owner in the release gap is preserved")
				return
			case "new-physical-consumer":
				var want map[string][]ovsdb.Row
				err := f.d.finishOVNDeviceUpdate(true, func() error {
					require.NoError(t, f.release())
					sw := fmt.Sprintf("incus-net%d-ls-int", f.networkID)
					retirementExec(t, f.raw,
						ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "consumer", Row: ovsdb.Row{"name": "unknown-consumer", "external_ids": retirementStringMap(map[string]string{"incus_switch": sw})}},
						ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: sw}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "consumer"}}}}}},
						ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(acl.OVNACLNetworkPortGroupName(f.aclID, f.networkID))}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "consumer"}}}}}})
					want = f.contents(t)
					return nil
				})
				require.Error(t, err)
				require.Equal(t, want, f.contents(t), "a new physical consumer in the release gap is preserved")
				return
			case "replacement-network":
				err := f.d.finishOVNDeviceUpdate(true, func() error {
					require.NoError(t, f.release())
					return f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
						require.NoError(t, tx.DeleteNetwork(ctx, "default", "acl-collection"))
						_, err := tx.CreateNetwork(ctx, "default", "acl-collection", "", db.NetworkTypeOVN, nil)
						return err
					})
				})
				require.Error(t, err)
				require.Equal(t, before, f.contents(t), "a numeric replacement cannot own original infrastructure")
				return
			case "no-constructor-groups":
				ops := []ovsdb.Operation{}
				for _, group := range f.groups {
					ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(group)}}})
				}

				for _, table := range []string{"Logical_Switch", "Logical_Router", "Logical_Switch_Port", "Logical_Router_Port"} {
					ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: retirementStringMap(map[string]string{})}}})
				}

				retirementExec(t, f.raw, ops...)
				want := f.contents(t)
				require.NoError(t, f.d.finishOVNDeviceUpdate(true, f.release), "unrelated ACL catalog rows do not require absent router infrastructure")
				require.Equal(t, want, f.contents(t))
				return
			default:
				// The previous generic collector reproduces the retained constructor group.
				require.NoError(t, acl.OVNPortGroupDeleteIfUnused(f.d.state, logger.AddContext(logger.Ctx{}), f.nb, "default", nil, ""))
				require.Equal(t, before, f.contents(t))
				require.NoError(t, f.d.finishOVNDeviceUpdate(true, f.release))
			}

			after := f.contents(t)
			require.Len(t, after["Port_Group"], 2, "only the network baseline and unrelated foreign group remain")
			require.Empty(t, after["ACL"], "retired constructor ACL rows are collected by native OVSDB")
			for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Port"} {
				require.ElementsMatch(t, before[table], after[table], table)
			}

			require.NoError(t, f.d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				devices, err := cluster.GetInstanceDevices(ctx, tx.Tx(), int(f.instanceID))
				require.NoError(t, err)
				for _, device := range devices {
					require.Empty(t, device.Config["security.acls"], "post-commit cleanup never rolls back the committed configuration")
				}

				var operations int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM networks_ovn_operations`).Scan(&operations))
				require.Zero(t, operations, "both original NIC and fresh update reservations are normally released")
				return nil
			}))
		})
	}
}
