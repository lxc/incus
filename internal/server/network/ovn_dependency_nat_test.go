//go:build linux && cgo && !agent

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
)

func dependencyTestNetwork(t *testing.T) *ovn {
	t.Helper()
	c := callersTestCluster(t)
	s := &state.State{ShutdownCtx: context.Background(), DB: &db.DB{Cluster: c}, OS: &sys.OS{MockMode: true}}
	s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
	var id int64
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, "default", "dependency-child", "", db.NetworkTypeOVN, map[string]string{"ipv4.address": "invalid"})
		if err != nil {
			return err
		}

		return tx.NetworkNodeCreated(id)
	}))
	loaded, err := LoadByName(s, "default", "dependency-child")
	require.NoError(t, err)
	n, ok := loaded.(*ovn)
	require.True(t, ok)
	previous := n.localState()
	available := IsAvailable(n.project, n.name)
	t.Cleanup(func() {
		n.setLocalState(previous)
		if available {
			n.setAvailable()
		} else {
			n.setUnavailable()
		}
	})
	n.setLocalState(ovnLocalState{started: true})
	n.setAvailable()
	return n
}

func TestOVNDependencyOperationAdmission(t *testing.T) {
	for _, name := range []string{"ready", "success", "evacuating", "evacuated", "restoring", "not-ready", "cluster-conflict", "local-conflict", "missing", "not-created"} {
		t.Run(name, func(t *testing.T) {
			n := dependencyTestNetwork(t)
			ctx := context.Background()
			var heldRelease func() error
			switch name {
			case "evacuating", "evacuated", "restoring":
				member := map[string]int{"evacuating": db.ClusterMemberStateEvacuating, "evacuated": db.ClusterMemberStateEvacuated, "restoring": db.ClusterMemberStateRestoring}[name]
				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.UpdateNodeStatus(tx.GetNodeID(), member) }))
			case "not-ready":
				n.setLocalState(ovnLocalState{})
			case "cluster-conflict":
				var err error
				heldRelease, _, err = AcquireOVNOperation(n.state, n.project, n.name, "update")
				require.NoError(t, err)
				defer func() { require.NoError(t, heldRelease()) }()
			case "local-conflict":
				unlock, err := LockOVNLifecycle(n.project, n.name)
				require.NoError(t, err)
				defer unlock()
			case "missing":
				n.name = "missing-child"
			case "not-created":
				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks SET state=0 WHERE id=?", n.id)
					return err
				}))
			}

			called := false
			effectError := errors.New("dependency effect failed")
			err := n.withDependencyOperation(func() error {
				called = true
				require.True(t, n.operationAuthorized)
				require.NotEmpty(t, n.ovnOperationToken)
				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
					require.Equal(t, n.ovnOperationToken, token)
					return err
				}))
				unlock, lockErr := LockOVNLifecycle(n.project, n.name)
				require.Nil(t, unlock)
				require.True(t, api.StatusErrorCheck(lockErr, http.StatusConflict))
				if name == "success" {
					return nil
				}

				return effectError
			})
			switch name {
			case "success":
				require.True(t, called)
				require.NoError(t, err)
			case "ready":
				require.True(t, called)
				require.ErrorIs(t, err, effectError)
			default:
				require.False(t, called, "refusal must precede the child effect")
				require.Error(t, err)
			}

			require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
				if name == "cluster-conflict" {
					require.NotEmpty(t, token, "the competing receipt is retained")
				} else {
					require.Empty(t, token, "the child's own receipt is released on failure")
				}

				return err
			}))
			if name != "local-conflict" {
				unlock, err := LockOVNLifecycle(n.project, n.name)
				require.NoError(t, err)
				unlock()
			}
		})
	}
}

func TestOVNNATProxyWithdrawalSharedRouter(t *testing.T) {
	for _, overlap := range []string{"none", "baseline", "nic-owner"} {
		t.Run(overlap, func(t *testing.T) {
			nb, raw, _ := callersTestNB(t)
			ctx := context.Background()
			parent := &ovn{common: common{id: 17, config: map[string]string{"ipv4.nat.address": "198.51.100.1", "ipv6.nat.address": "2001:db8::1", "ipv4.nat": "false"}}, ovnnb: nb}
			child := &ovn{common: common{id: 18, config: map[string]string{"ipv4.nat.address": "198.51.100.2", "ipv6.nat.address": "2001:db8::2"}}, parentID: 17, ovnnb: nb}
			port := parent.getExtSwitchRouterPortName()
			require.Equal(t, port, child.getExtSwitchRouterPortName())
			baseline := "203.0.113.9/32"
			if overlap == "baseline" {
				baseline += " 198.51.100.1/32"
			}

			callersExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "proxy", Row: ovsdb.Row{"name": string(port), "type": "router", "options": callersStringMap(map[string]string{"router-port": "shared-router", "arp_proxy": baseline, "foreign": "kept"})}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": string(parent.getExtSwitchName()), "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "proxy"}}}}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "sibling", Row: ovsdb.Row{"name": "sibling-proxy", "options": callersStringMap(map[string]string{"arp_proxy": "198.51.100.1/32"})}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "sibling-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "sibling"}}}}})
			prefixes := func(addresses ...string) []net.IPNet {
				out := []net.IPNet{}
				for _, address := range addresses {
					out = append(out, IPToNet(net.ParseIP(address)))
				}

				return out
			}

			all := prefixes("198.51.100.1", "198.51.100.2", "2001:db8::1", "2001:db8::2", "203.0.113.8")
			require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(ctx, port, all, nil))
			if overlap == "nic-owner" {
				callersExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "nic", Row: ovsdb.Row{"name": "retained-nic", "enabled": true, "external_ids": callersStringMap(map[string]string{"incus_location": "member"})}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "nic-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "nic"}}}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "nat-test_ip4"}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "nat-test_ip6"}})
				owner, err := nb.NewNICPrefixOwner(ctx, "nic-switch", "retained-nic", "member")
				require.NoError(t, err)
				require.NoError(t, nb.PublishNICPrefixes(ctx, "nat-test", nil, parent.getExtSwitchName(), port, prefixes("198.51.100.1"), owner))
			}

			selectOptions := func(selected networkOVN.OVNSwitchPort) map[string]string {
				result := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(selected)}}})
				require.Len(t, result[0].Rows, 1)
				options, ok := result[0].Rows[0]["options"].(ovsdb.OvsMap)
				require.True(t, ok)
				out := map[string]string{}
				for key, value := range options.GoMap {
					out[fmt.Sprint(key)] = fmt.Sprint(value)
				}

				return out
			}

			require.NoError(t, parent.deleteRouterNATAddressARPProxy())
			require.Contains(t, strings.Fields(selectOptions(port)["arp_proxy"]), "198.51.100.2/32", "parent removal preserves child contribution")
			require.NoError(t, child.deleteRouterNATAddressARPProxy())
			require.NoError(t, child.deleteRouterNATAddressARPProxy(), "exact withdrawal is retryable")
			// A network's own withdrawal also removes an equal entry that predates ownership tracking;
			// only a NIC owner's contribution keeps it.
			want := []string{"203.0.113.9/32", "203.0.113.8/32"}
			if overlap == "nic-owner" {
				want = append(want, "198.51.100.1/32")
			}

			options := selectOptions(port)
			require.ElementsMatch(t, want, strings.Fields(options["arp_proxy"]))
			require.Equal(t, "kept", options["foreign"])
			require.Equal(t, "198.51.100.1/32", selectOptions("sibling-proxy")["arp_proxy"])
			// Switching to routed ingress withdraws every proxy entry, as before; NIC owners keep receipts.
			require.NoError(t, nb.ClearLogicalSwitchPortARPProxy(ctx, port))
			require.Empty(t, strings.Fields(selectOptions(port)["arp_proxy"]))
			require.Equal(t, "kept", selectOptions(port)["foreign"])
			require.Equal(t, "198.51.100.1/32", selectOptions("sibling-proxy")["arp_proxy"])
		})
	}
}

func TestOVNDependencyOperationReleaseDebt(t *testing.T) {
	n := dependencyTestNetwork(t)
	ctx := context.Background()
	effectError := errors.New("dependency failed with notification debt")
	err := n.withDependencyOperation(func() error {
		require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.AddOVNNotification(ctx, "dependency-debt", n.ovnOperationToken)
		}))
		return effectError
	})
	require.ErrorIs(t, err, effectError)
	require.Contains(t, err.Error(), "unacknowledged notifications")
	require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		require.Equal(t, n.ovnOperationToken, token, "release failure preserves truthful ownership")
		active, err := tx.CancelOVNNotification(ctx, "dependency-debt")
		if err != nil {
			return err
		}

		require.False(t, active)
		return tx.ReleaseOVNNetworkOperation(ctx, n.project, n.name, token)
	}))
	unlock, err := LockOVNLifecycle(n.project, n.name)
	require.NoError(t, err)
	unlock()
}
