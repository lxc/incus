package ovn

import (
	"context"
	"fmt"
	"testing"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

func TestACLLiveDirectionalRuleReplacementRealBackend(t *testing.T) {
	for _, mode := range []string{"live-update", "foreign-rule", "foreign-project", "shared-rule", "switch-rule", "router-policy", "subject-race", "counterfeit-family", "missing-rule-owner", "wrong-rule-owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw := referenceTestNB(t)
			p := referenceStartedNIC(t, nb, raw)
			targets := map[OVNSwitchPort]NICConfigPublication{"incus-net17-instance-port": p}
			groups := []OVNPortGroup{"incus_acl29_all", "incus_acl29_ingress", "incus_acl29_ingress_reversed", "incus_acl29_egress", "incus_acl29_egress_reversed"}
			for _, group := range groups {
				require.NoError(t, nb.CreatePortGroup(ctx, 1, group, nil, "", "incus-net17-instance-port"))
				require.NoError(t, nb.UpdatePortGroupACLRules(ctx, group, nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "outport == @" + string(group), Priority: 0}))
			}

			guarded, err := nb.GuardACLReferenceUpdate(ctx, 1, 29, targets)
			require.NoError(t, err)
			// Like an ACL referencing itself, the first directional update creates a cross-group subject.
			require.NoError(t, guarded.UpdatePortGroupACLRules(ctx, groups[0], nil, OVNACLRule{Action: "allow", Direction: "to-lport", Match: "outport == @incus_acl29_all && inport == @incus_acl29_ingress", Priority: 123}))
			s, err := nb.physicalReferenceSnapshot(ctx)
			require.NoError(t, err)
			require.ErrorIs(t, s.groupUnusedExcept("incus_acl29_ingress", 1, map[string]bool{p.PortUUID: true}), ErrPhysicalReference, "the unchanged deletion predicate reproduces the original live-update refusal")
			original := referenceContents(t, raw)
			switch mode {
			case "missing-rule-owner", "wrong-rule-owner":
				group := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(groups[1])}}})[0].Rows[0]
				acls, err := physicalUUIDs(group["acls"])
				require.NoError(t, err)
				require.Len(t, acls, 1)
				ids := map[string]string{}
				if mode == "wrong-rule-owner" {
					ids[ovnExtIDIncusPortGroup] = "foreign"
				}

				for id := range acls {
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "ACL", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}, Row: ovsdb.Row{"match": "ip4", "external_ids": nicCleanupStringMapWire(ids)}})
				}

			case "counterfeit-family":
				require.NoError(t, nb.CreatePortGroup(ctx, 1, "incus_acl29_foreign", nil, ""))
				require.NoError(t, nb.UpdatePortGroupACLRules(ctx, "incus_acl29_foreign", nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "outport == @incus_acl29_ingress", Priority: 1}))
			case "foreign-rule":
				require.NoError(t, nb.CreatePortGroup(ctx, 1, "foreign", nil, ""))
				require.NoError(t, nb.UpdatePortGroupACLRules(ctx, "foreign", nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "outport == @incus_acl29_ingress", Priority: 1}))
			case "foreign-project":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(groups[0])}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "2"})}})
			case "shared-rule", "switch-rule":
				rule := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "ACL", Where: []ovsdb.Condition{{Column: "priority", Function: ovsdb.ConditionEqual, Value: 123}}})[0].Rows[0]["_uuid"]
				table := "Port_Group"
				if mode == "switch-rule" {
					table = "Logical_Switch"
				}

				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: ovsdb.Row{"name": "foreign", "acls": ovsdb.OvsSet{GoSet: []any{rule}}}})
			case "router-policy":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Policy", UUIDName: "policy", Row: ovsdb.Row{"action": "drop", "priority": 1, "match": "inport == @incus_acl29_ingress"}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": "foreign", "policies": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "policy"}}}}})
			case "subject-race":
				guard, validGuard := guarded.client.(*referenceMutationClient)
				require.True(t, validGuard)
				guard.Client = &referenceEffectClient{Client: guard.Client, before: func() {
					require.NoError(t, nb.CreatePortGroup(ctx, 1, "foreign", nil, ""))
					require.NoError(t, nb.UpdatePortGroupACLRules(ctx, "foreign", nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "outport == @incus_acl29_ingress", Priority: 1}))
				}}
			}

			before := referenceContents(t, raw)
			err = guarded.UpdatePortGroupACLRules(ctx, groups[1], nil, OVNACLRule{Action: "allow", Direction: "to-lport", Match: "outport == @incus_acl29_ingress && inport == @incus_acl29_egress", Priority: 124})
			if mode != "live-update" {
				require.Error(t, err)
				after := referenceContents(t, raw)
				if mode == "subject-race" {
					rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "ACL", Where: []ovsdb.Condition{{Column: "priority", Function: ovsdb.ConditionEqual, Value: 124}}})[0].Rows
					require.Empty(t, rows, "wait failure rolls back every inserted replacement rule")
					for _, row := range before["Port_Group"] {
						for _, current := range after["Port_Group"] {
							if row["_uuid"] == current["_uuid"] {
								require.Equal(t, row, current)
							}
						}
					}
				} else {
					referenceSameContents(t, before, after)
				}

				t.Logf("%s refused with original identities/rules retained: %v", mode, err)
				return
			}

			require.NoError(t, err)
			for i, group := range groups[2:] {
				require.NoError(t, guarded.UpdatePortGroupACLRules(ctx, group, nil, OVNACLRule{Action: "allow", Direction: "to-lport", Match: fmt.Sprintf("outport == @%s && inport == @incus_acl29_ingress", group), Priority: 125 + i}))
			}
			// A second admission and replacement must also work after all cross-direction rules exist.
			retry, err := nb.GuardACLReferenceUpdate(ctx, 1, 29, targets)
			require.NoError(t, err)
			require.NoError(t, retry.UpdatePortGroupACLRules(ctx, groups[0], nil, OVNACLRule{Action: "drop", Direction: "to-lport", Match: "outport == @incus_acl29_all", Priority: 0}))
			after := referenceContents(t, raw)
			for _, row := range original["Port_Group"] {
				found := false
				for _, current := range after["Port_Group"] {
					if row["_uuid"] == current["_uuid"] {
						found = true
						for _, field := range []string{"name", "ports", "external_ids"} {
							require.Equal(t, row[field], current[field])
						}
					}
				}

				require.True(t, found, "original group identity preserved")
			}

			for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "Address_Set", "Logical_Router_Policy", "Logical_Router"} {
				require.ElementsMatch(t, original[table], after[table], table)
			}

			require.Len(t, after["ACL"], 5, "native strong-reference GC removes every replaced old rule")
			t.Log("all five directional updates and repeat admission pass; group UUIDs/metadata/membership preserved")
		})
	}
}
