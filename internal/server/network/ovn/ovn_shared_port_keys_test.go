package ovn

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	model "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

// Network setup rewrites only the keys it owns on a shared router port, so a ledger or proxy entry
// another writer committed after this member's cache was read is preserved.
func TestSharedRouterPortKeysPreservedRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "port", Row: ovsdb.Row{"name": "ext-router", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "old"}), "options": nicCleanupStringMapWire(map[string]string{"router-port": "old"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "ext", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}}})
	require.Eventually(t, func() bool {
		return nb.get(context.Background(), &model.LogicalSwitchPort{Name: "ext-router"}) == nil
	}, 3*time.Second, 10*time.Millisecond)

	// Another writer adds its keys; this member may still see the earlier row in its cache.
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "ext-router"}}, Mutations: []ovsdb.Mutation{
		{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: nicCleanupStringMapWire(map[string]string{nicPrefixMetadata: "ledger"})},
		{Column: "options", Mutator: ovsdb.MutateOperationInsert, Value: nicCleanupStringMapWire(map[string]string{"arp_proxy": "192.0.2.10"})},
	}})

	require.NoError(t, nb.CreateLogicalSwitchPort(context.Background(), "ext", "ext-router", nil, true))
	require.NoError(t, nb.UpdateLogicalSwitchPortLinkRouter(context.Background(), "ext-router", "lrp-ext"))

	reply, err := raw.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "ext-router"}}})
	require.NoError(t, err)
	require.Len(t, reply[0].Rows, 1)
	row := reply[0].Rows[0]
	ids, err := nicCleanupStringMap(row["external_ids"])
	require.NoError(t, err)
	options, err := nicCleanupStringMap(row["options"])
	require.NoError(t, err)
	require.Equal(t, map[string]string{ovnExtIDIncusSwitch: "ext", nicPrefixMetadata: "ledger"}, ids)
	require.Equal(t, map[string]string{"arp_proxy": "192.0.2.10", "nat-addresses": "router", "router-port": "lrp-ext"}, options)
	require.Equal(t, "router", row["type"])
}

// A proxy update of a row without ownership records changes only arp_proxy, and only on the row
// it read; a concurrent writer's keys survive.
func TestSharedRouterPortARPProxyGuardedRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "port", Row: ovsdb.Row{"name": "ext-router", "options": nicCleanupStringMapWire(map[string]string{"arp_proxy": "192.0.2.1"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "ext", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}}})
	require.Eventually(t, func() bool {
		return nb.get(context.Background(), &model.LogicalSwitchPort{Name: "ext-router"}) == nil
	}, 3*time.Second, 10*time.Millisecond)

	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "ext-router"}}, Mutations: []ovsdb.Mutation{
		{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: nicCleanupStringMapWire(map[string]string{"other-writer": "kept"})},
	}})

	_, add, err := net.ParseCIDR("198.51.100.8/29")
	require.NoError(t, err)
	require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(context.Background(), "ext-router", []net.IPNet{*add}, nil))

	reply, err := raw.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "ext-router"}}})
	require.NoError(t, err)
	ids, err := nicCleanupStringMap(reply[0].Rows[0]["external_ids"])
	require.NoError(t, err)
	options, err := nicCleanupStringMap(reply[0].Rows[0]["options"])
	require.NoError(t, err)
	require.Equal(t, map[string]string{"other-writer": "kept"}, ids)
	require.Equal(t, map[string]string{"arp_proxy": "192.0.2.1 198.51.100.8/29"}, options)
}

// Route address set updates of sets without ownership records change only their addresses, and
// only on the rows they read; a concurrent writer's keys survive.
func TestAddressSetGuardedUpdateRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "incus_net9_routes_ip4", "addresses": ovsdb.OvsSet{GoSet: []any{"192.0.2.0/24"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "incus_net9_routes_ip6", "addresses": ovsdb.OvsSet{GoSet: []any{}}}})
	require.Eventually(t, func() bool {
		return nb.get(context.Background(), &model.AddressSet{Name: "incus_net9_routes_ip6"}) == nil
	}, 3*time.Second, 10*time.Millisecond)

	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_net9_routes_ip4"}}, Mutations: []ovsdb.Mutation{
		{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: nicCleanupStringMapWire(map[string]string{"other-writer": "kept"})},
	}})

	_, added, err := net.ParseCIDR("198.51.100.0/24")
	require.NoError(t, err)
	_, removed, err := net.ParseCIDR("192.0.2.0/24")
	require.NoError(t, err)
	require.NoError(t, nb.UpdateAddressSetAdd(context.Background(), "incus_net9_routes", *added))
	require.NoError(t, nb.UpdateAddressSetRemove(context.Background(), "incus_net9_routes", *removed))

	reply, err := raw.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_net9_routes_ip4"}}})
	require.NoError(t, err)
	ids, err := nicCleanupStringMap(reply[0].Rows[0]["external_ids"])
	require.NoError(t, err)
	require.Equal(t, map[string]string{"other-writer": "kept"}, ids)
	// A one-element set is encoded as its atom.
	require.Equal(t, "198.51.100.0/24", reply[0].Rows[0]["addresses"])
}

// An addition of no addresses still creates missing sets, as network setup repair relies on.
func TestAddressSetEmptyAdditionCreatesSetsRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	require.NoError(t, nb.UpdateAddressSetAdd(context.Background(), "incus_net8_routes"))
	for _, name := range []string{"incus_net8_routes_ip4", "incus_net8_routes_ip6"} {
		reply, err := raw.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: name}}})
		require.NoError(t, err)
		require.Len(t, reply[0].Rows, 1, name)
	}

	require.Error(t, nb.UpdateAddressSetRemove(context.Background(), "incus_net7_routes"), "removals still require the sets")
}
