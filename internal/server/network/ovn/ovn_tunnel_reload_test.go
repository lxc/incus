package ovn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// tunnelReloadNetwork seeds the infrastructure the driver creates for an OVN network with a router.
func tunnelReloadNetwork(t *testing.T, nb *NB, networkID int64) (OVNRouter, OVNSwitch, string) {
	t.Helper()
	ctx := context.Background()
	router := OVNRouter(fmt.Sprintf("incus-net%d-lr", networkID))
	routerPort := OVNRouterPort(fmt.Sprintf("incus-net%d-lr-lrp-int", networkID))
	sw := OVNSwitch(fmt.Sprintf("incus-net%d-ls-int", networkID))
	require.NoError(t, nb.CreateLogicalRouter(ctx, router, false))
	require.NoError(t, nb.CreateLogicalSwitch(ctx, sw, false))
	require.NoError(t, nb.CreateLogicalSwitchPort(ctx, sw, OVNSwitchPort(string(sw)+"-lsp-router"), &OVNSwitchPortOpts{RouterPort: routerPort}, false))
	return router, sw, string(routerPort)
}

// tunnelReloadPort creates the tunnel port exactly as createOVNTunnel does.
func tunnelReloadPort(nb *NB, sw OVNSwitch, name string) error {
	return nb.CreateLogicalSwitchPort(context.Background(), sw, OVNSwitchPort(name), &OVNSwitchPortOpts{IPV4: "none", IPV6: "none", Promiscuous: true}, true)
}

// TestTunnelSharedReloadRealBackend covers a global tunnel update: a notified peer creates
// its tunnel port during the origin's guarded shared reload, or the tunnel already exists.
func TestTunnelSharedReloadRealBackend(t *testing.T) {
	modes := []string{"peer-creates-during-reload", "tunnel-exists-before-reload", "unplanned-tunnel", "planned-name-nic-addresses", "planned-name-foreign-ids", "planned-name-typed"}
	for _, relay := range []bool{false, true} {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("relay-%t/%s", relay, mode), func(t *testing.T) {
				ctx := context.Background()
				nb, raw := referenceTestNB(t, relay)
				router, sw, routerPort := tunnelReloadNetwork(t, nb, 6)
				planned := OVNSwitchPort("tunnel-cr07-a-ok-f-0")
				origin := nb.WithNetworkTunnelPorts(planned)
				if mode == "tunnel-exists-before-reload" {
					require.NoError(t, tunnelReloadPort(nb, sw, string(planned)))
				}

				targets := map[OVNSwitchPort]NICConfigPublication{}
				observed, err := origin.CheckNetworkNICReplay(ctx, 6, routerPort, targets)
				require.NoError(t, err)
				guarded, err := origin.GuardNetworkNICReplay(ctx, 6, routerPort, targets, targets, observed)
				require.NoError(t, err)
				switch mode {
				case "peer-creates-during-reload":
					require.NoError(t, tunnelReloadPort(nb, sw, string(planned)))
				case "unplanned-tunnel":
					require.NoError(t, tunnelReloadPort(nb, sw, "tunnel-other-net-x-0"))
				case "planned-name-nic-addresses":
					require.NoError(t, nb.CreateLogicalSwitchPort(ctx, sw, planned, &OVNSwitchPortOpts{IPV4: "192.0.2.10"}, true))
				case "planned-name-foreign-ids":
					require.NoError(t, tunnelReloadPort(nb, sw, string(planned)))
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(planned)}}, Mutations: []ovsdb.Mutation{{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsMap{GoMap: map[any]any{ovnExtIDIncusLocation: "peer"}}}}})
				case "planned-name-typed":
					require.NoError(t, tunnelReloadPort(nb, sw, string(planned)))
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(planned)}}, Row: ovsdb.Row{"type": "localport"}})
				}

				err = guarded.UpdateLogicalRouterMulticastRelay(ctx, router, true)
				t.Logf("origin guarded multicast relay result: %v", err)
				result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Router", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(router)}}, Columns: []string{"options"}})
				options, mapErr := nicCleanupStringMap(result[0].Rows[0]["options"])
				require.NoError(t, mapErr)
				if mode == "peer-creates-during-reload" || mode == "tunnel-exists-before-reload" {
					require.NoError(t, err)
					require.Equal(t, "true", options["mcast_relay"])
					return
				}

				// A relay may still serve the pre-mutation row; the content wait then refuses instead.
				require.Error(t, err)
				if !relay || mode == "unplanned-tunnel" || mode == "planned-name-nic-addresses" {
					require.ErrorContains(t, err, "New physical consumer entered shared reload")
				}

				require.Empty(t, options["mcast_relay"])
			})
		}
	}
}

// guardMoveClient applies one backend change just before each guarded mutation is sent.
type guardMoveClient struct {
	ovsdbClient.Client
	move  func(attempt int)
	sends int
}

func (c *guardMoveClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range ops {
		if op.Op == ovsdb.OperationDelete && op.Table == "Logical_Switch" {
			c.sends++
			c.move(c.sends)
			break
		}
	}

	return c.Client.Transact(ctx, ops...)
}

// TestPhysicalGuardMovedRealBackend covers an unrelated write that moves the whole-table guard
// snapshot before dispatch: the rejection has no effect and a re-planned call succeeds. A relevant
// change is refused by re-validation instead, and root/fence waits are never classified.
func TestPhysicalGuardMovedRealBackend(t *testing.T) {
	for _, relay := range []bool{false, true} {
		for _, mode := range []string{"unrelated", "relevant-port"} {
			t.Run(fmt.Sprintf("relay-%t/%s", relay, mode), func(t *testing.T) {
				ctx := context.Background()
				nb, raw := referenceTestNB(t, relay)
				_, sw, routerPort := tunnelReloadNetwork(t, nb, 6)
				referenceExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "other", Row: ovsdb.Row{"name": "unrelated-port"}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "unrelated-ls", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "other"}}}}})
				client := &guardMoveClient{Client: nb.client}
				client.move = func(attempt int) {
					if attempt != 1 {
						return
					}

					if mode == "unrelated" {
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "unrelated-port"}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{"unrelated": "changed"})}})
						return
					}

					require.NoError(t, nb.CreateLogicalSwitchPort(ctx, sw, "incus-net6-instance-late-eth0", &OVNSwitchPortOpts{}, false))
				}

				moving := *nb
				moving.client = client
				switchRows := func() int {
					result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(sw)}}, Columns: []string{"_uuid"}})
					return len(result[0].Rows)
				}

				err := moving.GuardNetworkDelete(6, routerPort).DeleteLogicalSwitch(ctx, sw)
				require.Error(t, err)
				require.True(t, PhysicalGuardMoved(err), "%v", err)
				require.Equal(t, 1, switchRows())
				if mode == "relevant-port" {
					// Re-planning only re-validates: the new live port is refused before effects.
					err = moving.GuardNetworkDelete(6, routerPort).DeleteLogicalSwitch(ctx, sw)
					require.ErrorIs(t, err, ErrPhysicalReference)
					require.Equal(t, 1, switchRows())
					return
				}

				require.NoError(t, moving.GuardNetworkDelete(6, routerPort).DeleteLogicalSwitch(ctx, sw))
				require.Equal(t, 0, switchRows())
			})
		}
	}
}

func TestPhysicalGuardMovedClassification(t *testing.T) {
	zero := 0
	physical := ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: nicCleanupStringMapWire(map[string]string{})}}, Until: "=="}
	root := nicCleanupRootWait("8d2e2ab0-0b8c-4d31-9a63-2f4f2a0f3d11")
	fence := ovsdb.Operation{Op: ovsdb.OperationWait, Table: "NB_Global", Timeout: &zero, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{"incus-fence": "1"}}}}, Until: "=="}
	other := physical
	other.Table = "NB_Global"
	for _, tt := range []struct {
		op     ovsdb.Operation
		fenced bool
		moved  bool
	}{
		{physical, false, true},
		{physical, true, false},
		{root, false, false},
		{fence, false, false},
		{other, false, false},
	} {
		_, err := ovsdb.CheckOperationResults([]ovsdb.OperationResult{{Error: "timed out"}}, []ovsdb.Operation{tt.op})
		operationErrors, _ := ovsdb.CheckOperationResults([]ovsdb.OperationResult{{Error: "timed out"}}, []ovsdb.Operation{tt.op})
		for _, operationErr := range operationErrors {
			err = errors.Join(err, operationErr)
		}

		if tt.fenced {
			err = errors.Join(backendDB.ErrFenced, err)
		}

		require.Equal(t, tt.moved, PhysicalGuardMoved(err), "%s %v", tt.op.Table, err)
	}
}

// TestDNSRecordReattachRealBackend covers a start after a failed-start revert detached and cleared
// the port's DNS record: the update must reattach it so its names are served and later retired.
func TestDNSRecordReattachRealBackend(t *testing.T) {
	ctx := context.Background()
	nb, raw := referenceTestNB(t)
	_, sw, _ := tunnelReloadNetwork(t, nb, 6)
	ip := []net.IP{net.ParseIP("10.0.0.5")}
	first, err := nb.UpdateLogicalSwitchPortDNS(ctx, sw, "incus-net6-instance-x-eth0", "x.example", ip)
	require.NoError(t, err)
	attached := func() bool {
		result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(sw)}}, Columns: []string{"dns_records"}})
		records, err := physicalUUIDs(result[0].Rows[0]["dns_records"])
		require.NoError(t, err)
		return records[string(first)]
	}

	require.True(t, attached())
	require.NoError(t, nb.DeleteLogicalSwitchPortDNS(ctx, sw, first, false))
	require.False(t, attached())
	require.Eventually(t, func() bool {
		uuid, _, _, err := nb.GetLogicalSwitchPortDNS(ctx, "incus-net6-instance-x-eth0")
		return err == nil && uuid == first
	}, 3*time.Second, 10*time.Millisecond)

	again, err := nb.UpdateLogicalSwitchPortDNS(ctx, sw, "incus-net6-instance-x-eth0", "x.example", ip)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.True(t, attached())
	result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "DNS", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: string(first)}}}, Columns: []string{"records"}})
	records, err := nicCleanupStringMap(result[0].Rows[0]["records"])
	require.NoError(t, err)
	require.Equal(t, "10.0.0.5", records["x.example"])
}

// TestPhysicalGuardIgnoresNorthdColumns covers northd binding an unrelated port during a guarded
// deletion: the guard does not move and the deletion proceeds.
func TestPhysicalGuardIgnoresNorthdColumns(t *testing.T) {
	ctx := context.Background()
	nb, raw := referenceTestNB(t)
	_, sw, routerPort := tunnelReloadNetwork(t, nb, 6)
	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "other", Row: ovsdb.Row{"name": "unrelated-port"}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "unrelated-ls", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "other"}}}}})
	client := &guardMoveClient{Client: nb.client}
	client.move = func(attempt int) {
		if attempt == 1 {
			referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "unrelated-port"}}, Row: ovsdb.Row{"up": ovsdb.OvsSet{GoSet: []any{true}}, "dynamic_addresses": "00:16:3e:00:00:01 192.0.2.5"}})
		}
	}

	moving := *nb
	moving.client = client
	require.NoError(t, moving.GuardNetworkDelete(6, routerPort).DeleteLogicalSwitch(ctx, sw))
}
