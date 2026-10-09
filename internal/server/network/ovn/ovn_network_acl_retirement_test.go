package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	model "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

func retirementFixture(t *testing.T, parent int64) (*NB, ovsdbClient.Client, NetworkACLRetirement) {
	t.Helper()
	nb, raw := referenceTestNB(t)
	owner := int64(17)
	if parent != 0 {
		owner = parent
	}

	router := fmt.Sprintf("incus-net%d-lr", owner)
	lrp := router + "-lrp-int"
	if parent != 0 {
		lrp += "-net17"
	}

	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Port", UUIDName: "lrp", Row: ovsdb.Row{"name": lrp, "mac": "00:11:22:33:44:55", "networks": ovsdb.OvsSet{GoSet: []any{"192.0.2.1/24"}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": router, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "lrp"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "router", Row: ovsdb.Row{"name": "incus-net17-ls-int-lsp-router", "type": "router", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"}), "options": nicCleanupStringMapWire(map[string]string{"router-port": lrp})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net17-ls-int", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "router"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "2"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_acl31_net19", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusProjectID: "1"})}})
	require.Eventually(t, func() bool {
		port := &model.LogicalSwitchPort{Name: "incus-net17-ls-int-lsp-router"}
		return nb.get(context.Background(), port) == nil
	}, 3*time.Second, 10*time.Millisecond)
	associated := []OVNPortGroup{"incus_acl29_all", "incus_acl29_ingress", "incus_acl29_ingress_reversed", "incus_acl29_egress", "incus_acl29_egress_reversed"}
	for _, name := range associated {
		require.NoError(t, nb.CreatePortGroup(context.Background(), 1, name, nil, ""))
	}

	// Exercise the real constructor, including its retained router member and associations.
	require.NoError(t, nb.CreatePortGroup(context.Background(), 1, "incus_acl29_net17", associated, "incus-net17-ls-int", "incus-net17-ls-int-lsp-router"))
	return nb, raw, NetworkACLRetirement{ProjectID: 1, NetworkID: 17, ParentID: parent, Token: uuid.NewString(), Root: nb.BackendID(), ACLIDs: []int64{29}}
}

func retirementContents(t *testing.T, raw ovsdbClient.Client) map[string][]ovsdb.Row {
	t.Helper()
	rows := referenceContents(t, raw)
	rows["Logical_Router_Port"] = referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Router_Port", Where: []ovsdb.Condition{}})[0].Rows
	witness, err := json.Marshal(rows)
	require.NoError(t, err)
	t.Logf("actual retirement table/UUID witness: %s", witness)
	return rows
}

func TestNetworkACLRetirementConstructorRealBackend(t *testing.T) {
	for _, parent := range []int64{0, 23} {
		t.Run(fmt.Sprint(parent), func(t *testing.T) {
			nb, raw, plan := retirementFixture(t, parent)
			want := retirementContents(t, raw)
			require.ErrorIs(t, nb.CheckACLPhysicalUnused(context.Background(), 1, 29), ErrPhysicalReference)
			require.NoError(t, nb.DeletePortGroup(context.Background(), "incus_acl29_net17"))
			referenceSameContents(t, want, retirementContents(t, raw))
			require.NoError(t, retirementDelete(nb, plan))
			for _, row := range want["Port_Group"] {
				if row["name"] == "incus_acl29_net17" {
					referenceRemoveUUID(want, "Port_Group", row["_uuid"].(ovsdb.UUID))
				}
			}

			referenceSameContents(t, want, retirementContents(t, raw))
			require.NoError(t, nb.CheckACLPhysicalUnused(context.Background(), 1, 29))
			require.NoError(t, retirementDelete(nb, plan))
			referenceSameContents(t, want, retirementContents(t, raw))
		})
	}
}

func TestNetworkACLRetirementRefusalsRealBackend(t *testing.T) {
	for _, change := range []string{"project", "association", "disabled-consumer", "subject", "sibling-router-parent", "multiple-switch-parent", "malformed-producer", "parent", "root", "token", "invalid-second-acl"} {
		t.Run(change, func(t *testing.T) {
			nb, raw, plan := retirementFixture(t, 23)
			switch change {
			case "project", "association":
				ids := map[string]string{ovnExtIDIncusProjectID: "1", ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusPortGroup: "incus_acl29_all,incus_acl29_ingress,incus_acl29_ingress_reversed,incus_acl29_egress,incus_acl29_egress_reversed"}
				if change == "project" {
					ids[ovnExtIDIncusProjectID] = "2"
				} else {
					ids[ovnExtIDIncusSwitch] = "incus-net19-ls-int"
				}

				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			case "disabled-consumer":
				referenceExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "consumer", Row: ovsdb.Row{"name": "incus-net17-consumer", "enabled": ovsdb.OvsSet{GoSet: []any{false}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusLocation: "source"}), "addresses": ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:55 192.0.2.9"}}}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "consumer"}}}}}})
				port := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-consumer"}}})[0].Rows[0]["_uuid"]
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{port}}}}})

			case "subject":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "subject", Row: ovsdb.Row{"action": "drop", "direction": "to-lport", "priority": 1, "match": "inport == @incus_acl29_net17"}}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "foreign"}}, Row: ovsdb.Row{"acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "subject"}}}}})
			case "sibling-router-parent", "multiple-switch-parent":
				childTable, parentTable, childName := "Logical_Router_Port", "Logical_Router", "incus-net23-lr-lrp-int-net17"
				if change == "multiple-switch-parent" {
					childTable, parentTable, childName = "Logical_Switch_Port", "Logical_Switch", "incus-net17-ls-int-lsp-router"
				}

				child := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: childTable, Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: childName}}})[0].Rows[0]["_uuid"]
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: parentTable, Row: ovsdb.Row{"name": "foreign-parent", "ports": ovsdb.OvsSet{GoSet: []any{child}}}})
			case "malformed-producer":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int-lsp-router"}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", nicConfigPublicationKey: "invalid-json"})}})
			case "parent":
				plan.ParentID = 0
			case "root":
				plan.Root = uuid.NewString()
			case "token":
				plan.Token = ""
			case "invalid-second-acl":
				plan.ACLIDs = append(plan.ACLIDs, 0)
			}

			want := retirementContents(t, raw)
			require.Error(t, retirementDelete(nb, plan))
			referenceSameContents(t, want, retirementContents(t, raw))
		})
	}
}

func TestNetworkACLRetirementEffectBoundaryRealBackend(t *testing.T) {
	for _, race := range []string{"group-uuid", "new-subject", "router-parent", "root", "lost-reply", "new-publication", "retained-publication"} {
		t.Run(race, func(t *testing.T) {
			nb, raw, plan := retirementFixture(t, 0)
			if race == "retained-publication" {
				retirementPublication(t, nb, raw, "pending")
			}

			want := retirementContents(t, raw)
			wrapped := &referenceEffectClient{Client: nb.client, loseReply: race == "lost-reply"}
			wrapped.before = func() {
				switch race {
				case "new-publication":
					retirementPublication(t, nb, raw, "pending")
				case "retained-publication":
					port := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-consumer"}}})[0].Rows[0]
					ids, err := nicCleanupStringMap(port["external_ids"])
					require.NoError(t, err)
					publication, err := decodeNICConfig(ids[nicConfigPublicationKey])
					require.NoError(t, err)
					publication.Generation = uuid.NewString()
					ids[nicConfigPublicationKey], err = publicationWire(publication)
					require.NoError(t, err)
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: port["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
				case "group-uuid":
					var group ovsdb.Row
					for _, row := range want["Port_Group"] {
						if row["name"] == "incus_acl29_net17" {
							group = row
						}
					}

					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: group["_uuid"]}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": group["name"], "ports": group["ports"], "external_ids": group["external_ids"]}})
				case "new-subject":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "subject", Row: ovsdb.Row{"action": "drop", "direction": "to-lport", "priority": 1, "match": "inport == @incus_acl29_net17"}}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "foreign"}}, Row: ovsdb.Row{"acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "subject"}}}}})
				case "router-parent":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-lr"}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{"changed": "after-snapshot"})}})
				case "root":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: nb.BackendID()}}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})
				}

				want = retirementContents(t, raw)
			}

			nb.client = wrapped
			retained, err := nb.DeleteNetworkACLPortGroups(context.Background(), plan)
			require.Nil(t, retained)
			require.Nil(t, wrapped.before)
			if race == "lost-reply" {
				require.True(t, errors.Is(err, context.DeadlineExceeded))
				for _, row := range want["Port_Group"] {
					if row["name"] == "incus_acl29_net17" {
						referenceRemoveUUID(want, "Port_Group", row["_uuid"].(ovsdb.UUID))
					}
				}

				wrapped.loseReply = false
				require.NoError(t, retirementDelete(nb, plan))
			} else {
				require.ErrorContains(t, err, "timed out")
			}

			referenceSameContents(t, want, retirementContents(t, raw))
		})
	}
}

func retirementDelete(nb *NB, plan NetworkACLRetirement) error {
	_, err := nb.DeleteNetworkACLPortGroups(context.Background(), plan)
	return err
}

func retirementPublication(t *testing.T, nb *NB, raw ovsdbClient.Client, phase string) ovsdb.Row {
	t.Helper()
	referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "consumer", Row: ovsdb.Row{"name": "incus-net17-consumer", "enabled": ovsdb.OvsSet{GoSet: []any{false}}, "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int", ovnExtIDIncusLocation: "source"}), "addresses": ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:55 192.0.2.9"}}}},
		ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "consumer"}}}}}})
	input := NICConfigPublication{Generation: uuid.NewString(), NetworkID: 17, ProjectID: 1, InstanceUUID: uuid.NewString(), Device: "eth0", Source: "source", Phase: phase, Input: map[string]string{"hwaddr": "00:11:22:33:44:55"}, ACLIDs: map[string]int64{"original": 29}, MAC: "00:11:22:33:44:55", IPs: []string{"192.0.2.9"}}
	require.NoError(t, nb.PublishNICConfig(context.Background(), "incus-net17-ls-int", "incus-net17-consumer", input))
	return referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-consumer"}}})[0].Rows[0]
}

func TestNetworkACLRetirementRetainedPublicationRealBackend(t *testing.T) {
	for _, control := range []string{"add", "start", "pending", "member", "absent-group", "mixed"} {
		t.Run(control, func(t *testing.T) {
			nb, raw, plan := retirementFixture(t, 23)
			phase := control
			if phase != "add" && phase != "start" && phase != "pending" {
				phase = "pending"
			}

			port := retirementPublication(t, nb, raw, phase)
			if control == "member" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{port["_uuid"]}}}}})
			}

			if control == "absent-group" {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}})
			}

			if control == "mixed" {
				associated := []OVNPortGroup{"incus_acl30_all", "incus_acl30_ingress", "incus_acl30_ingress_reversed", "incus_acl30_egress", "incus_acl30_egress_reversed"}
				for _, name := range associated {
					require.NoError(t, nb.CreatePortGroup(context.Background(), 1, name, nil, ""))
				}

				require.NoError(t, nb.CreatePortGroup(context.Background(), 1, "incus_acl30_net17", associated, "incus-net17-ls-int", "incus-net17-ls-int-lsp-router"))
				plan.ACLIDs = append(plan.ACLIDs, 30)
			}

			want := retirementContents(t, raw)
			require.ErrorIs(t, nb.CheckACLPhysicalUnused(context.Background(), 1, 29), ErrPhysicalReference)
			retained, err := nb.DeleteNetworkACLPortGroups(context.Background(), plan)
			require.NoError(t, err)
			require.Equal(t, []int64{29}, retained)
			if control == "mixed" {
				for _, row := range want["Port_Group"] {
					if row["name"] == "incus_acl30_net17" {
						referenceRemoveUUID(want, "Port_Group", row["_uuid"].(ovsdb.UUID))
					}
				}
			}

			referenceSameContents(t, want, retirementContents(t, raw))
			require.ErrorIs(t, nb.CheckACLPhysicalUnused(context.Background(), 1, 29), ErrPhysicalReference)
			// Remove only this fixture's publication to verify later normal collection.
			if control == "pending" {
				ids, err := nicCleanupStringMap(port["external_ids"])
				require.NoError(t, err)
				delete(ids, nicConfigPublicationKey)
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: port["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
				want = retirementContents(t, raw)
				retained, err = nb.DeleteNetworkACLPortGroups(context.Background(), plan)
				require.NoError(t, err)
				require.Empty(t, retained)
				for _, row := range want["Port_Group"] {
					if row["name"] == "incus_acl29_net17" {
						referenceRemoveUUID(want, "Port_Group", row["_uuid"].(ovsdb.UUID))
					}
				}

				referenceSameContents(t, want, retirementContents(t, raw))
				require.NoError(t, nb.CheckACLPhysicalUnused(context.Background(), 1, 29))
			}
		})
	}
}

func TestNetworkACLRetirementRetainedOwnershipRefusalsRealBackend(t *testing.T) {
	for _, control := range []string{"project", "port", "switch", "group-project", "subject", "unpublished-member"} {
		t.Run(control, func(t *testing.T) {
			nb, raw, plan := retirementFixture(t, 23)
			port := retirementPublication(t, nb, raw, "pending")
			ids, err := nicCleanupStringMap(port["external_ids"])
			require.NoError(t, err)
			publication, err := decodeNICConfig(ids[nicConfigPublicationKey])
			require.NoError(t, err)
			switch control {
			case "project":
				publication.ProjectID = 2
			case "port":
				publication.PortUUID = uuid.NewString()
			case "switch":
				publication.SwitchUUID = uuid.NewString()
			case "group-project":
				group := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}})[0].Rows[0]
				groupIDs, err := nicCleanupStringMap(group["external_ids"])
				require.NoError(t, err)
				groupIDs[ovnExtIDIncusProjectID] = "2"
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: group["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(groupIDs)}})
			case "subject":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "subject", Row: ovsdb.Row{"action": "drop", "direction": "to-lport", "priority": 1, "match": "inport == @incus_acl29_net17"}}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "foreign"}}, Row: ovsdb.Row{"acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "subject"}}}}})
			case "unpublished-member":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "unknown", Row: ovsdb.Row{"name": "incus-net17-unknown", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"})}}, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "unknown"}}}}}}, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "unknown"}}}}}})
			}

			ids[nicConfigPublicationKey], err = publicationWire(publication)
			require.NoError(t, err)
			referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: port["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
			want := retirementContents(t, raw)
			retained, err := nb.DeleteNetworkACLPortGroups(context.Background(), plan)
			require.Error(t, err)
			require.Nil(t, retained)
			referenceSameContents(t, want, retirementContents(t, raw))
		})
	}
}
