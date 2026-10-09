package ovn

import (
	"context"
	"maps"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

func referenceKnownNIC(t *testing.T, nb *NB, raw ovsdbClient.Client) NICConfigPublication {
	t.Helper()
	port, sw := referencePort(t, raw)
	p := NICConfigPublication{Generation: uuid.NewString(), NetworkID: 17, ProjectID: 1, InstanceUUID: uuid.NewString(), Device: "eth0", Source: "source", Phase: "add", Input: NICConfigInputs(map[string]string{"network": "n", "hwaddr": "00:11:22:33:44:55", "ipv4.address": "192.0.2.9"}), ACLIDs: map[string]int64{"old": 29}}
	require.NoError(t, nb.PublishNICConfig(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port", p))
	p.PortUUID = port
	p.SwitchUUID = sw
	return p
}

func TestNICRetirementExactOriginalRealBackend(t *testing.T) {
	nb, raw := referenceTestNB(t)
	p := referenceKnownNIC(t, nb, raw)
	result := referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_acl29_all", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: p.PortUUID}}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "1"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "DNS", UUIDName: "dns", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitchPort: "incus-net17-instance-port"}), "records": nicCleanupStringMapWire(map[string]string{"host": "192.0.2.9"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "QoS", UUIDName: "qos", Row: ovsdb.Row{"direction": "from-lport", "match": "inport == \"incus-net17-instance-port\"", "priority": 1, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitchPort: "incus-net17-instance-port"})}},
		ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.SwitchUUID}}}, Row: ovsdb.Row{"dns_records": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "dns"}}}, "qos_rules": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "qos"}}}, "other_config": nicCleanupStringMapWire(map[string]string{"exclude_ips": "192.0.2.9 192.0.2.20..192.0.2.30", "foreign": "retained"})}})
	require.NotEmpty(t, result)
	foreign := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "foreign_set", "addresses": ovsdb.OvsSet{GoSet: []any{"198.51.100.3"}}}})[0].UUID
	require.ErrorIs(t, nb.CheckNetworkPhysicalUnused(context.Background(), 17, "router"), ErrPhysicalReference)
	require.NoError(t, nb.RetireNICConfig(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port", p, true))
	require.NoError(t, nb.CheckNetworkPhysicalUnused(context.Background(), 17, "router"))
	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.SwitchUUID}}}}, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: foreign}}})
	cfg, err := nicCleanupStringMap(rows[0].Rows[0]["other_config"])
	require.NoError(t, err)
	require.Equal(t, "192.0.2.20..192.0.2.30", cfg["exclude_ips"])
	require.Equal(t, "retained", cfg["foreign"])
	require.Len(t, rows[1].Rows, 1)
	for _, table := range []string{"Logical_Switch_Port", "DNS", "QoS"} {
		rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}})
		require.Empty(t, rows[0].Rows)
	}
}

func TestNICRetirementMutationRaceRealBackend(t *testing.T) {
	for _, mode := range []string{"replacement", "foreign-private-parent", "lost-reply"} {
		t.Run(mode, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			p := referenceKnownNIC(t, nb, raw)
			originalClient := nb.client
			client := &referenceEffectClient{Client: originalClient}
			nb.client = client
			switch mode {
			case "replacement":
				client.before = func() {
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "changed"})}})
				}

			case "foreign-private-parent":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "private", Row: ovsdb.Row{"direction": "to-lport", "match": "0", "priority": 1, "action": "drop", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitchPort: "incus-net17-instance-port"})}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign", "acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "private"}}}}})
			case "lost-reply":
				client.loseReply = true
			}

			err := nb.RetireNICConfig(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port", p, true)
			require.Error(t, err)
			if mode == "lost-reply" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}

			if mode != "lost-reply" {
				rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{}})
				require.Len(t, rows[0].Rows, 1)
			}
		})
	}
}

func referenceStartedNIC(t *testing.T, nb *NB, raw ovsdbClient.Client) NICConfigPublication {
	t.Helper()
	ctx := context.Background()
	p := referenceKnownNIC(t, nb, raw)
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.PortUUID}}}, Row: ovsdb.Row{"enabled": ovsdb.OvsSet{GoSet: []any{true}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "incus_net17_routes_ip4"}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "incus_net17_routes_ip6"}})
	owner, err := nb.NewNICPrefixOwner(ctx, "incus-net17-ls-int", "incus-net17-instance-port", "source")
	require.NoError(t, err)
	require.NoError(t, nb.PublishNICPrefixes(ctx, "incus_net17_routes", nil, "", "", nil, owner))
	p.Phase = "start"
	p.Generation = owner.Generation
	require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
	return p
}

func TestSharedReplayPinnedConsumerAndEffectRaceRealBackend(t *testing.T) {
	for _, mode := range []string{"success", "foreign-acl-receipt", "new-consumer-race", "disabled", "disabled-add", "enabled-race", "generation-race", "settings-race", "lost-reply", "omitted", "pending"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw := referenceTestNB(t)
			p := referenceStartedNIC(t, nb, raw)
			if mode == "disabled-add" {
				require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, "incus-net17-instance-port", false))
				p.Phase = "add"
				require.NoError(t, nb.PublishNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
			}

			targets := map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p}
			planned := maps.Clone(targets)
			next := p
			next.ACLIDs = map[string]int64{"new": 31}
			planned["incus-net17-instance-port"] = next
			if mode == "disabled" {
				require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, "incus-net17-instance-port", false))
			}

			if mode == "omitted" {
				targets = map[OVNSwitchPort]NICConfigPublication{}
			}

			if mode == "pending" {
				require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", "incus-net17-instance-port"))
			}

			observed, err := nb.CheckNetworkNICReplay(ctx, 17, "router", targets)
			if mode == "omitted" || mode == "pending" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if mode == "new-consumer-race" {
				nb.client = &referenceEffectClient{Client: nb.client, before: func() {
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "late", Row: ovsdb.Row{"name": "incus-net17-instance-late"}}, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.SwitchUUID}}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "late"}}}}}})
				}}
			}

			guarded, err := nb.GuardNetworkNICReplay(ctx, 17, "router", targets, planned, observed)
			require.NoError(t, err)
			err = guarded.BeginNICConfigPublication(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
			if mode == "new-consumer-race" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if mode == "foreign-acl-receipt" {
				_, row, e := nb.publicationPort(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
				require.NoError(t, e)
				ids, e := nicCleanupStringMap(row["external_ids"])
				require.NoError(t, e)
				receipt, e := decodeNICConfig(ids[nicConfigPublicationKey])
				require.NoError(t, e)
				receipt.ACLIDs = map[string]int64{"foreign": 99}
				ids[nicConfigPublicationKey], e = publicationWire(receipt)
				require.NoError(t, e)
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			}

			if mode == "enabled-race" || mode == "generation-race" || mode == "settings-race" {
				guardClient, validGuard := guarded.client.(*referenceMutationClient)
				require.True(t, validGuard)
				guardClient.Client = &referenceEffectClient{Client: guardClient.Client, before: func() {
					if mode == "enabled-race" {
						require.NoError(t, rawUpdateEnabled(t, raw, p.PortUUID, false))
						return
					}

					_, row, e := nb.publicationPort(ctx, "incus-net17-ls-int", "incus-net17-instance-port")
					require.NoError(t, e)
					ids, e := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, e)
					receipt, e := decodeNICConfig(ids[nicConfigPublicationKey])
					require.NoError(t, e)
					if mode == "settings-race" {
						row["addresses"] = ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:99 192.0.2.99"}}
						receipt.Settings = publicationSettings(row)
					} else {
						receipt.Generation = uuid.NewString()
					}

					ids[nicConfigPublicationKey], e = publicationWire(receipt)
					require.NoError(t, e)
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids), "addresses": row["addresses"]}})
				}}
			}

			if mode == "lost-reply" {
				guardClient, validGuard := guarded.client.(*referenceMutationClient)
				require.True(t, validGuard)
				guardClient.Client = &referenceEffectClient{Client: guardClient.Client, loseReply: true}
			}

			_, prefix, _ := net.ParseCIDR("198.51.100.0/24")
			err = guarded.UpdateAddressSetAdd(ctx, "incus_net17_routes", *prefix)
			if mode == "foreign-acl-receipt" || mode == "enabled-race" || mode == "generation-race" || mode == "settings-race" || mode == "lost-reply" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if mode == "disabled-add" {
				wrong := next
				wrong.Phase = "start"
				require.ErrorContains(t, guarded.CompleteNICConfigReload(ctx, "incus-net17-ls-int", "incus-net17-instance-port", wrong), "captured original phase")
			}

			next.Phase = p.Phase
			require.NoError(t, guarded.CompleteNICConfigReload(ctx, "incus-net17-ls-int", "incus-net17-instance-port", next))
			require.NoError(t, guarded.UpdateAddressSetAdd(ctx, "incus_net17_routes", *prefix))
			_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", planned)
			require.NoError(t, err)
		})
	}
}

func TestMigrationConfigReceiptActualSharedPlanRealBackend(t *testing.T) {
	ctx := context.Background()
	nb, raw := referenceTestNB(t)
	p := referenceStartedNIC(t, nb, raw)
	port, err := nb.CaptureNICPortCleanup(ctx, "incus-net17-ls-int", "incus-net17-instance-port", "source")
	require.NoError(t, err)
	plan, err := nb.CaptureNICMigrationShared(ctx, uuid.NewString(), "target", uuid.NewString(), "target-chassis", port, "", "incus_net17_routes", nil, "", "", nil)
	require.NoError(t, err)
	targets := map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p}
	_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", targets)
	require.NoError(t, err)
	original := nb.client
	nb.client = &referenceEffectClient{Client: original, loseReply: true}
	require.NoError(t, nb.ApplyNICMigrationShared(ctx, plan)) // Exact shared-plan target verification acknowledges the owned committed transfer.
	nb.client = original
	require.NoError(t, nb.VerifyNICMigrationShared(ctx, plan, true))
	_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", targets)
	require.Error(t, err) // Placement still selected at source: target-ready is not applicable there.
	p.Source = "target"
	targets["incus-net17-instance-port"] = p
	_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", targets)
	require.NoError(t, err)
	// A failed/uncertain handover cannot rewrite the exact target receipt or original rollback bytes.
	corrupted := plan
	corrupted.Rows = append([]NICMigrationSharedRow{}, plan.Rows...)
	for i := range corrupted.Rows {
		if corrupted.Rows[i].Table == "Logical_Switch_Port" && corrupted.Rows[i].Before["_uuid"] == (ovsdb.UUID{GoUUID: port.PortUUID}) {
			corrupted.Rows[i].After = maps.Clone(corrupted.Rows[i].After)
			ids, e := nicCleanupStringMap(corrupted.Rows[i].After["external_ids"])
			require.NoError(t, e)
			ids[nicConfigPublicationKey] = "foreign"
			corrupted.Rows[i].After["external_ids"] = nicCleanupStringMapWire(ids)
		}
	}

	require.Error(t, nb.ApplyNICMigrationShared(ctx, corrupted))
	require.NoError(t, nb.RollbackNICMigrationShared(ctx, plan))
	p.Source = "source"
	targets["incus-net17-instance-port"] = p
	_, err = nb.CheckNetworkNICReplay(ctx, 17, "router", targets)
	require.NoError(t, err)
}

func TestPendingNICProducerRetainsOriginalACLAfterMembershipRemoval(t *testing.T) {
	ctx := context.Background()
	nb, raw := referenceTestNB(t)
	p := referenceStartedNIC(t, nb, raw)
	require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p))
	require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 1, 29), ErrPhysicalReference)
	_, err := nb.GuardACLReferenceUpdate(ctx, 1, 29, map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p})
	require.Error(t, err)
	require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, "incus-net17-instance-port", false))
	require.Error(t, nb.RetireNICConfig(ctx, "incus-net17-ls-int", "incus-net17-instance-port", p, true))
	t.Log("Pending producer retains original ACL without any PG membership, and refuses ambiguous replay/retirement")
}

func TestLegacyProducerAdoptionRefusedBeforeEffects(t *testing.T) {
	nb, raw := referenceTestNB(t)
	referencePort(t, raw)
	require.ErrorContains(t, nb.BeginNICConfigPublication(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port"), "legacy adoption is unsupported")
}

func rawUpdateEnabled(t *testing.T, raw ovsdbClient.Client, port string, enabled bool) error {
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: port}}}, Row: ovsdb.Row{"enabled": ovsdb.OvsSet{GoSet: []any{enabled}}}})
	return nil
}

func TestRetainedNetworkACLGroupExactReuseAndRaceRealBackend(t *testing.T) {
	for _, mode := range []string{"success", "foreign", "extra-member", "wrong-router-option", "extra-parent", "replacement-race", "router-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw := referenceTestNB(t)
			p := referenceKnownNIC(t, nb, raw)
			routerName := OVNSwitchPort("incus-net17-ls-int-lsp-router")
			router := referenceExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "router", Row: ovsdb.Row{"name": string(routerName), "type": "router", "options": nicCleanupStringMapWire(map[string]string{"router-port": "incus-net17-lr-lrp-int"}), "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"})}},
				ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: p.SwitchUUID}}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "router"}}}}}})[0].UUID
			require.Eventually(t, func() bool {
				id, err := nb.GetLogicalSwitchPortUUID(ctx, routerName)
				return err == nil && string(id) == router.GoUUID
			}, 5*time.Second, time.Millisecond)
			group := OVNPortGroup("incus_acl29_net17")
			associated := []OVNPortGroup{"incus_acl29_all"}
			require.NoError(t, nb.CreatePortGroup(ctx, 1, group, associated, "incus-net17-ls-int", routerName))
			rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(group)}}})[0].Rows
			require.Len(t, rows, 1)
			original := rows[0]
			if mode == "foreign" {
				ids, err := nicCleanupStringMap(original["external_ids"])
				require.NoError(t, err)
				ids[ovnExtIDIncusProjectID] = "2"
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: original["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			}

			if mode == "extra-member" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: original["_uuid"]}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: p.PortUUID}}}}}})
			}

			if mode == "wrong-router-option" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: router}}, Row: ovsdb.Row{"options": nicCleanupStringMapWire(map[string]string{"router-port": "incus-net18-lr-lrp-int"})}})
			}

			if mode == "extra-parent" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net18-ls-int", "ports": ovsdb.OvsSet{GoSet: []any{router}}}})
			}

			guarded, reused, err := nb.GuardExistingPortGroup(ctx, 1, group, associated, 17, routerName)
			if mode == "foreign" || mode == "extra-member" || mode == "wrong-router-option" || mode == "extra-parent" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.True(t, reused)
			if mode == "replacement-race" || mode == "router-race" {
				guard, validGuard := guarded.client.(*referenceMutationClient)
				require.True(t, validGuard)
				guard.Client = &referenceEffectClient{Client: guard.Client, before: func() {
					if mode == "router-race" {
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: router}}, Row: ovsdb.Row{"options": nicCleanupStringMapWire(map[string]string{"router-port": "foreign"})}})
						return
					}

					replacement := maps.Clone(original)
					delete(replacement, "_uuid")
					delete(replacement, "_version")
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: original["_uuid"]}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: replacement})
				}}
			}

			err = guarded.UpdatePortGroupACLRules(ctx, group, nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "ip4", Priority: 1})
			if mode != "success" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			current := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(group)}}})[0].Rows[0]
			require.Equal(t, original["_uuid"], current["_uuid"])
			require.Equal(t, original["ports"], current["ports"])
			require.Equal(t, original["external_ids"], current["external_ids"])
		})
	}
}
