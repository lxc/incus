package ovn

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

// A rejected parent change used to invalidate an otherwise unchanged running route producer.
// This real backend witness isolates that phase write from any Stop/migration/host cleanup path.
func TestNICParentRefusalPendingProducerWitness(t *testing.T) {
	nb, raw := referenceTestNB(t)
	ctx := context.Background()
	p := referenceStartedNIC(t, nb, raw)
	p.Input = maps.Clone(p.Input)
	p.Input["ipv4.routes"] = "192.0.2.123/32"
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
	original, err := nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
	require.NoError(t, err)
	require.Equal(t, "start", original.Phase)

	// Exact old Update ordering: Begin precedes the pure admission check's refusal.
	require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", "incus-net17-instance-port"))
	pending, err := nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Phase)
	pending.Phase = original.Phase
	require.Equal(t, original, pending)
	t.Logf("Native old-order witness: root=%s switch=%s port=%s generation=%s source=%s only phase=start->pending, original inputs=%v", original.RootUUID, original.SwitchUUID, original.PortUUID, original.Generation, original.Source, original.Input)

	_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p})
	require.Error(t, err)
	// Normal route removal reaches retirement only after its successful existing Stop boundary.
	require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, "incus-net17-instance-port", false))
	err = nb.RetireNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p, false)
	require.ErrorContains(t, err, "NIC backend producer differs from the selected original configuration or placement")
	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: original.PortUUID}}}})
	require.Len(t, rows[0].Rows, 1)
}

func TestNICRunningRouteOriginalRetirementGuards(t *testing.T) {
	for _, change := range []string{"original", "route", "hwaddr", "network", "project", "instance", "device", "source", "generation", "atomic-race"} {
		t.Run(change, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			ctx := context.Background()
			p := referenceStartedNIC(t, nb, raw)
			p.Input = maps.Clone(p.Input)
			p.Input["ipv4.routes"] = "192.0.2.123/32"
			require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
			require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, "incus-net17-instance-port", false))
			target := p
			target.Input = maps.Clone(p.Input)
			switch change {
			case "route":
				delete(target.Input, "ipv4.routes")
			case "hwaddr":
				target.Input["hwaddr"] = "00:11:22:33:44:99"
			case "network":
				target.NetworkID++
			case "project":
				target.ProjectID++
			case "instance":
				target.InstanceUUID = uuid.NewString()
			case "device":
				target.Device = "eth1"
			case "source", "generation":
				rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}, Columns: []string{"external_ids"}})
				ids, err := nicCleanupStringMap(rows[0].Rows[0]["external_ids"])
				require.NoError(t, err)
				if change == "source" {
					ids[ovnExtIDIncusLocation] = "foreign-source"
				} else {
					var producer NICConfigPublication
					require.NoError(t, json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &producer))
					producer.Generation = uuid.NewString()
					ids[nicConfigPublicationKey] = nicPrefixEncode(producer)
				}

				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			case "atomic-race":
				nb.client = &referenceEffectClient{Client: nb.client, before: func() {
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}, Row: ovsdb.Row{"options": nicCleanupStringMapWire(map[string]string{"foreign": "changed"})}})
				}}
			}

			err := nb.RetireNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", target, false)
			rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}})
			if change == "original" {
				require.NoError(t, err)
				require.Empty(t, rows[0].Rows)
			} else {
				require.Error(t, err)
				require.Len(t, rows[0].Rows, 1)
			}
		})
	}
}
