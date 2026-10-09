package ovn

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"testing"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

type parentPrefixEffectClient struct {
	ovsdbClient.Client
	before    func()
	loseReply bool
	calls     int
}

func (c *parentPrefixEffectClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range ops {
		if op.Op != ovsdb.OperationUpdate {
			continue
		}

		c.calls++
		if c.before != nil {
			before := c.before
			c.before = nil
			before()
		}

		result, err := c.Client.Transact(ctx, ops...)
		if c.loseReply && err == nil {
			_, resultErr := ovsdb.CheckOperationResults(result, ops)
			if resultErr == nil {
				return nil, context.DeadlineExceeded
			}
		}

		return result, err
	}

	return c.Client.Transact(ctx, ops...)
}

type parentPrefixPreparer interface {
	PrepareNICParentPrefixReload(context.Context, OVNSwitch, OVNSwitchPort, OVNSwitch, OVNSwitchPort) (func(context.Context, OVNSwitch, map[OVNSwitchPort]NICConfigPublication) error, error)
}

func parentPrefixRow(t *testing.T, raw ovsdbClient.Client, name string) ovsdb.Row {
	t.Helper()
	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: name}}})[0].Rows
	require.Len(t, rows, 1)
	return rows[0]
}

func parentPrefixFixture(t *testing.T, prefixes []net.IPNet) (*NB, ovsdbClient.Client, NICConfigPublication, NICPrefixOwner) {
	t.Helper()
	ctx := context.Background()
	nb, raw := referenceTestNB(t)
	p := referenceStartedNIC(t, nb, raw)
	for _, id := range []int{23, 41} {
		sw := fmt.Sprintf("incus-net%d-ls-ext", id)
		port := sw + "-lsp-router"
		referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "proxy", Row: ovsdb.Row{"name": port, "type": "router", "addresses": nicCleanupStringSetWire([]string{"router"}), "options": nicCleanupStringMapWire(map[string]string{"router-port": fmt.Sprintf("incus-net%d-lr-lrp-ext", id)}), "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: sw, "foreign": "preserved"})}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "proxy"}}}}})
	}

	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "router", Row: ovsdb.Row{"name": "incus-net17-ls-int-lsp-router", "type": "router", "addresses": nicCleanupStringSetWire([]string{"router"}), "options": nicCleanupStringMapWire(map[string]string{"router-port": "incus-net23-lr-lrp-int-net17", "nat-addresses": "router"}), "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"})}}, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "router"}}}}}})
	row := parentPrefixRow(t, raw, "incus-net17-instance-port")
	ids, err := nicCleanupStringMap(row["external_ids"])
	require.NoError(t, err)
	var owner NICPrefixOwner
	require.NoError(t, json.Unmarshal([]byte(ids[nicPrefixGeneration]), &owner))
	require.NoError(t, nb.PublishNICPrefixes(ctx, "incus_net17_routes", nil, "incus-net23-ls-ext", "incus-net23-ls-ext-lsp-router", prefixes, owner))
	return nb, raw, p, owner
}

func parentPrefixBegin(t *testing.T, nb *NB, p NICConfigPublication, additional ...NICConfigPublication) (*NB, map[OVNSwitchPort]NICConfigPublication, func(context.Context, OVNSwitch, map[OVNSwitchPort]NICConfigPublication) error) {
	t.Helper()
	ctx := context.Background()
	targets := map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p}
	for _, producer := range additional {
		targets[OVNSwitchPort("incus-net17-instance-port-"+producer.Device)] = producer
	}

	guarded, err := nb.GuardNetworkNICReplayParentChange(ctx, 17, "incus-net23-lr-lrp-int-net17", "incus-net41-lr-lrp-int-net17", targets, targets)
	require.NoError(t, err)
	// The original implementation has only per-producer completion; it omits shared provenance.
	complete := func(ctx context.Context, sw OVNSwitch, targets map[OVNSwitchPort]NICConfigPublication) error {
		for port, target := range targets {
			err := guarded.CompleteNICConfigReload(ctx, sw, port, target)
			if err != nil {
				return err
			}
		}

		return nil
	}

	preparer, supported := any(guarded).(parentPrefixPreparer)
	if supported {
		complete, err = preparer.PrepareNICParentPrefixReload(ctx, "incus-net23-ls-ext", "incus-net23-ls-ext-lsp-router", "incus-net41-ls-ext", "incus-net41-ls-ext-lsp-router")
		require.NoError(t, err)
	}

	for port := range targets {
		require.NoError(t, guarded.BeginNICConfigPublication(ctx, "incus-net17-ls-int", port))
	}

	require.NoError(t, guarded.UpdateLogicalSwitchPortLinkRouter(ctx, "incus-net17-ls-int-lsp-router", "incus-net41-lr-lrp-int-net17"))
	return guarded, targets, complete
}

func TestOrdinaryParentReloadPrefixPublication(t *testing.T) {
	_, ip, _ := net.ParseCIDR("192.0.2.9/32")
	for _, addresses := range [][]net.IPNet{nil, {*ip}} {
		t.Run(fmt.Sprintf("prefixes-%d", len(addresses)), func(t *testing.T) {
			nb, raw, p, owner := parentPrefixFixture(t, addresses)
			original := referenceContents(t, raw)
			_, targets, complete := parentPrefixBegin(t, nb, p)
			require.NoError(t, complete(context.Background(), "incus-net17-ls-int", targets))
			oldLedger, oldIDs, err := nb.nicPrefixLedger("Logical_Switch_Port", parentPrefixRow(t, raw, "incus-net23-ls-ext-lsp-router"), false)
			require.NoError(t, err)
			nextLedger, nextIDs, err := nb.nicPrefixLedger("Logical_Switch_Port", parentPrefixRow(t, raw, "incus-net41-ls-ext-lsp-router"), false)
			require.NoError(t, err, "successful ordinary reparent must publish the selected allocation on its new parent proxy")
			_, oldActive := oldLedger.Owners[owner.Generation]
			require.False(t, oldActive)
			require.Equal(t, owner, nextLedger.Owners[owner.Generation].Owner)
			require.Equal(t, nicPrefixRequested(addresses, 0), nextLedger.Owners[owner.Generation].Prefixes)
			require.Empty(t, oldLedger.Released)
			require.Empty(t, nextLedger.Released)
			require.Equal(t, "preserved", oldIDs["foreign"])
			require.Equal(t, "preserved", nextIDs["foreign"])
			actual, err := nb.NICConfigPublicationOf(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port")
			require.NoError(t, err)
			require.Equal(t, "start", actual.Phase)
			require.Equal(t, owner.Generation, actual.Generation)
			require.Equal(t, owner.PortUUID, actual.PortUUID)
			require.Equal(t, p.Source, actual.Source)
			for _, table := range []string{"Address_Set", "NB_Global"} {
				require.ElementsMatch(t, original[table], referenceContents(t, raw)[table], table)
			}
		})
	}
}

func TestOrdinaryParentReloadPrefixAtomicRefusals(t *testing.T) {
	for _, change := range []string{"wrong-phase", "missing-acls", "missing-producer", "source", "generation", "old-contribution", "old-proxy", "new-proxy", "prefix-marker", "atomic-race", "lost-reply"} {
		t.Run(change, func(t *testing.T) {
			nb, raw, p, _ := parentPrefixFixture(t, nil)
			guarded, targets, complete := parentPrefixBegin(t, nb, p)
			var effect *parentPrefixEffectClient
			switch change {
			case "wrong-phase":
				wrong := targets["incus-net17-instance-port"]
				wrong.Phase = "add"
				targets["incus-net17-instance-port"] = wrong
			case "missing-acls":
				wrong := targets["incus-net17-instance-port"]
				wrong.ACLIDs = nil
				targets["incus-net17-instance-port"] = wrong
			case "missing-producer":
				delete(targets, "incus-net17-instance-port")
			case "source", "generation", "prefix-marker":
				row := parentPrefixRow(t, raw, "incus-net17-instance-port")
				ids, err := nicCleanupStringMap(row["external_ids"])
				require.NoError(t, err)
				switch change {
				case "source":
					ids[ovnExtIDIncusLocation] = "foreign-source"
				case "prefix-marker":
					delete(ids, nicPrefixGeneration)
				default:
					producer, err := decodeNICConfig(ids[nicConfigPublicationKey])
					require.NoError(t, err)
					producer.Generation = "113b9885-748a-4d1b-9803-02df3eb90ed9"
					ids[nicConfigPublicationKey] = nicPrefixEncode(producer)
				}

				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			case "old-contribution":
				row := parentPrefixRow(t, raw, "incus-net23-ls-ext-lsp-router")
				ledger, ids, err := nb.nicPrefixLedger("Logical_Switch_Port", row, false)
				require.NoError(t, err)
				ledger.Owners = map[string]nicPrefixContribution{}
				referenceExec(t, raw, nicPrefixUpdate("Logical_Switch_Port", row, ledger, ids))
			case "old-proxy", "new-proxy":
				id := 23
				if change == "new-proxy" {
					id = 41
				}

				name := fmt.Sprintf("incus-net%d-ls-ext-lsp-router", id)
				row := maps.Clone(parentPrefixRow(t, raw, name))
				delete(row, "_uuid")
				delete(row, "_version")
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: name}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "replacement", Row: row}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: fmt.Sprintf("incus-net%d-ls-ext", id)}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "replacement"}}}}})
			case "atomic-race", "lost-reply":
				client, valid := guarded.client.(*referenceMutationClient)
				require.True(t, valid)
				effect = &parentPrefixEffectClient{Client: client.Client, loseReply: change == "lost-reply", before: func() {
					if change == "atomic-race" {
						row := parentPrefixRow(t, raw, "incus-net41-ls-ext-lsp-router")
						ids, err := nicCleanupStringMap(row["external_ids"])
						require.NoError(t, err)
						ids["foreign-race"] = "preserved"
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
					}
				}}
				client.Client = effect
			}

			before := referenceContents(t, raw)
			err := complete(context.Background(), "incus-net17-ls-int", targets)
			if effect != nil {
				require.Equal(t, 1, effect.calls, "the actual atomic update boundary was intercepted")
			}

			if change == "lost-reply" {
				require.NoError(t, err, "exact atomic committed result acknowledges the lost reply")
				return
			}

			require.Error(t, err)
			after := referenceContents(t, raw)
			if change == "atomic-race" {
				for _, row := range after["Logical_Switch_Port"] {
					ids, err := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, err)
					delete(ids, "foreign-race")
					row["external_ids"] = nicCleanupStringMapWire(ids)
				}

				for _, snapshot := range []map[string][]ovsdb.Row{before, after} {
					for _, rows := range snapshot {
						for _, row := range rows {
							delete(row, "_version")
						}
					}
				}
			}

			referenceSameContents(t, before, after)
		})
	}
}

func TestOrdinaryParentReloadPreservesOtherContributions(t *testing.T) {
	nb, raw, p, owner := parentPrefixFixture(t, nil)
	before := map[string]nicPrefixLedger{}
	for _, name := range []string{"incus-net23-ls-ext-lsp-router", "incus-net41-ls-ext-lsp-router"} {
		row := parentPrefixRow(t, raw, name)
		ledger, ids, err := nb.nicPrefixLedger("Logical_Switch_Port", row, true)
		require.NoError(t, err)
		ledger.Baseline = []string{"198.51.100.0/24"}
		ledger.Managed = []string{"203.0.113.0/24"}
		sibling := owner
		sibling.PortUUID, sibling.Generation, sibling.Source = uuid.NewString(), uuid.NewString(), "sibling-source"
		ledger.Owners[sibling.Generation] = nicPrefixContribution{Owner: sibling, Prefixes: []string{"192.0.2.10/32"}}
		retired := sibling
		retired.Generation = uuid.NewString()
		ledger.Released[retired.Generation] = nicPrefixContribution{Owner: retired, Prefixes: []string{"192.0.2.11/32"}}
		referenceExec(t, raw, nicPrefixUpdate("Logical_Switch_Port", row, ledger, ids))
		before[name] = ledger
	}

	_, targets, complete := parentPrefixBegin(t, nb, p)
	require.NoError(t, complete(context.Background(), "incus-net17-ls-int", targets))
	for name, want := range before {
		actual, ids, err := nb.nicPrefixLedger("Logical_Switch_Port", parentPrefixRow(t, raw, name), false)
		require.NoError(t, err)
		delete(want.Owners, owner.Generation)
		delete(actual.Owners, owner.Generation)
		require.Equal(t, want, actual)
		require.Equal(t, "preserved", ids["foreign"])
	}
}

func TestOrdinaryParentReloadBatchNoPartialCompletion(t *testing.T) {
	ctx := context.Background()
	nb, raw, p, _ := parentPrefixFixture(t, nil)
	name := OVNSwitchPort("incus-net17-instance-port-eth1")
	row := maps.Clone(parentPrefixRow(t, raw, "incus-net17-instance-port"))
	delete(row, "_uuid")
	delete(row, "_version")
	row["name"] = string(name)
	row["external_ids"] = nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusLocation: p.Source})
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "second", Row: row}, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "second"}}}}}})
	owner, err := nb.NewNICPrefixOwner(ctx, "incus-net17-ls-int", name, p.Source)
	require.NoError(t, err)
	require.NoError(t, nb.PublishNICPrefixes(ctx, "incus_net17_routes", nil, "incus-net23-ls-ext", "incus-net23-ls-ext-lsp-router", nil, owner))
	second := p
	second.Device, second.Generation = "eth1", owner.Generation
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", name, second))
	second, err = nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", name)
	require.NoError(t, err)
	_, targets, complete := parentPrefixBegin(t, nb, p, second)
	wrong := targets[name]
	wrong.Phase = "add"
	targets[name] = wrong
	before := referenceContents(t, raw)
	require.Error(t, complete(ctx, "incus-net17-ls-int", targets))
	referenceSameContents(t, before, referenceContents(t, raw))
	targets[name] = second
	require.NoError(t, complete(ctx, "incus-net17-ls-int", targets))
	for port := range targets {
		actual, err := nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", port)
		require.NoError(t, err)
		require.Equal(t, "start", actual.Phase)
	}

	ledger, _, err := nb.nicPrefixLedger("Logical_Switch_Port", parentPrefixRow(t, raw, "incus-net41-ls-ext-lsp-router"), false)
	require.NoError(t, err)
	require.Len(t, ledger.Owners, 2)
}

func TestOrdinaryParentPrefixMissingProvenanceRefusesBeforeEffects(t *testing.T) {
	nb, raw, p, _ := parentPrefixFixture(t, nil)
	row := parentPrefixRow(t, raw, "incus-net23-ls-ext-lsp-router")
	ids, err := nicCleanupStringMap(row["external_ids"])
	require.NoError(t, err)
	delete(ids, nicPrefixMetadata)
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
	targets := map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p}
	guarded, err := nb.GuardNetworkNICReplayParentChange(context.Background(), 17, "incus-net23-lr-lrp-int-net17", "incus-net41-lr-lrp-int-net17", targets, targets)
	require.NoError(t, err)
	preparer, supported := any(guarded).(parentPrefixPreparer)
	require.True(t, supported)
	before := referenceContents(t, raw)
	_, err = preparer.PrepareNICParentPrefixReload(context.Background(), "incus-net23-ls-ext", "incus-net23-ls-ext-lsp-router", "incus-net41-ls-ext", "incus-net41-ls-ext-lsp-router")
	require.ErrorContains(t, err, "Shared prefix provenance is unknown")
	referenceSameContents(t, before, referenceContents(t, raw))
	actual, err := nb.NICConfigPublicationOf(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port")
	require.NoError(t, err)
	require.Equal(t, "start", actual.Phase)
}

func TestOrdinaryParentReloadFinalSourceLocation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, changed := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy-%t/changed-%t", legacy, changed), func(t *testing.T) {
				ctx := context.Background()
				nb, raw, p, _ := parentPrefixFixture(t, nil)
				if legacy {
					p.Legacy, p.Generation = true, p.PortUUID
					require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
					row := parentPrefixRow(t, raw, "incus-net17-instance-port")
					ids, err := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, err)
					delete(ids, nicPrefixGeneration)
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
					proxy := parentPrefixRow(t, raw, "incus-net23-ls-ext-lsp-router")
					proxyIDs, err := nicCleanupStringMap(proxy["external_ids"])
					require.NoError(t, err)
					delete(proxyIDs, nicPrefixMetadata)
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: proxy["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(proxyIDs)}})
				}

				_, targets, complete := parentPrefixBegin(t, nb, p)
				if changed {
					row := parentPrefixRow(t, raw, "incus-net17-instance-port")
					ids, err := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, err)
					original := maps.Clone(ids)
					ids[ovnExtIDIncusLocation] = "foreign-location"
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
					currentIDs, err := nicCleanupStringMap(parentPrefixRow(t, raw, "incus-net17-instance-port")["external_ids"])
					require.NoError(t, err)
					// The native producer receipt and marker remain exactly the selected originals.
					require.Equal(t, original[nicConfigPublicationKey], currentIDs[nicConfigPublicationKey])
					require.Equal(t, original[nicPrefixGeneration], currentIDs[nicPrefixGeneration])
				}

				before := referenceContents(t, raw)
				err := complete(ctx, "incus-net17-ls-int", targets)
				if changed {
					require.ErrorContains(t, err, "NIC producer source location changed")
					referenceSameContents(t, before, referenceContents(t, raw))
					actual, err := nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
					require.NoError(t, err)
					require.Equal(t, "pending", actual.Phase)
					return
				}

				require.NoError(t, err)
				actual, err := nb.NICConfigPublicationOf(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
				require.NoError(t, err)
				require.Equal(t, "start", actual.Phase)
				require.Equal(t, p.Source, actual.Source)
				require.Equal(t, p.Generation, actual.Generation)
				require.Equal(t, legacy, actual.Legacy)
				if legacy {
					row := parentPrefixRow(t, raw, "incus-net17-instance-port")
					ids, err := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, err)
					require.Empty(t, ids[nicPrefixGeneration])
					for _, name := range []string{"incus-net23-ls-ext-lsp-router", "incus-net41-ls-ext-lsp-router"} {
						for _, original := range before["Logical_Switch_Port"] {
							if original["name"] == name {
								require.Equal(t, original, parentPrefixRow(t, raw, name))
							}
						}
					}
				}
			})
		}
	}
}
