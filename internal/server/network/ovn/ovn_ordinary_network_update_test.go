package ovn

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

func ordinaryNetworkFixture(t *testing.T, parent int64) (*NB, ovsdbClient.Client) {
	t.Helper()
	nb, raw, _ := retirementFixture(t, parent)
	candidate := NetworkReferenceInfrastructure{ProjectID: 1, NetworkID: 17, ParentID: parent}
	require.NoError(t, nb.UpdateLogicalSwitchPortLinkRouter(context.Background(), "incus-net17-ls-int-lsp-router", OVNRouterPort(candidate.RouterPort())))
	return nb, raw
}

func ordinaryRouterUpdate(port string, options map[string]string) ovsdb.Operation {
	return ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: port}}, Row: ovsdb.Row{"options": nicCleanupStringMapWire(options)}}
}

func ordinaryACLUpdate(nb *NB) error {
	return nb.UpdatePortGroupACLRules(context.Background(), "incus_acl29_net17", nil, OVNACLRule{Direction: "to-lport", Action: "drop", Match: "ip4", Priority: 123})
}

func ordinaryForeignChange(t *testing.T, raw ovsdbClient.Client, change string) {
	t.Helper()
	const port = "incus-net17-ls-int-lsp-router"
	switch change {
	case "option":
		referenceExec(t, raw, ordinaryRouterUpdate(port, map[string]string{"router-port": "incus-net41-lr-lrp-int-net17", "foreign": "retained"}))
	case "wrong-parent":
		referenceExec(t, raw, ordinaryRouterUpdate(port, map[string]string{"router-port": "incus-net42-lr-lrp-int-net17"}))
	case "replacement":
		row := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: port}}})[0].Rows[0]
		delete(row, "_uuid")
		delete(row, "_version")
		referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: port}}},
			ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "replacement", Row: row},
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "replacement"}}}}},
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "replacement"}}}}})
	case "new-consumer":
		referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "foreign", Row: ovsdb.Row{"name": "foreign-consumer", "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "incus-net17-ls-int"})}},
			ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "foreign"}}}}}},
			ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "foreign"}}}}}})
	case "switch-parent":
		portUUID := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: port}}})[0].Rows[0]["_uuid"]
		referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{portUUID}}}})
	case "removed":
		referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{}}}},
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus_acl29_net17"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{}}}},
			ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: port}}})
	default:
		t.Fatalf("unknown change %q", change)
	}
}

func TestOrdinaryParentChangeRealBackend(t *testing.T) {
	nb, raw := ordinaryNetworkFixture(t, 23)
	ctx := context.Background()
	const oldPort = "incus-net23-lr-lrp-int-net17"
	const newPort = "incus-net41-lr-lrp-int-net17"
	const port = "incus-net17-ls-int-lsp-router"
	legacy, err := nb.GuardNetworkNICReplay(ctx, 17, oldPort, nil, nil)
	require.NoError(t, err)
	require.NoError(t, legacy.UpdateLogicalSwitchPortLinkRouter(ctx, port, newPort))
	require.ErrorContains(t, ordinaryACLUpdate(legacy), "New physical consumer entered shared reload", "the original caller reproduces the supported parent-change failure")
	require.NoError(t, nb.UpdateLogicalSwitchPortLinkRouter(ctx, port, oldPort))
	guarded, err := nb.GuardNetworkNICReplayParentChange(ctx, 17, oldPort, newPort, nil, nil)
	require.NoError(t, err)
	before := referenceContents(t, raw)
	require.NoError(t, guarded.CreateLogicalSwitchPort(ctx, "incus-net17-ls-int", port, nil, true))
	require.NoError(t, guarded.UpdateLogicalSwitchPortLinkRouter(ctx, port, newPort))
	require.NoError(t, ordinaryACLUpdate(guarded))
	after := referenceContents(t, raw)
	require.Equal(t, before["Logical_Switch_Port"][0]["_uuid"], after["Logical_Switch_Port"][0]["_uuid"])
	for _, table := range []string{"Logical_Switch", "Logical_Router", "Logical_Router_Policy", "Address_Set"} {
		require.ElementsMatch(t, before[table], after[table], table)
	}

	rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "ACL", Where: []ovsdb.Condition{{Column: "priority", Function: ovsdb.ConditionEqual, Value: 123}}})[0].Rows
	require.Len(t, rows, 1, "the selected mutation actually reached native OVSDB")
}

func TestOrdinaryParentChangeRefusalsRealBackend(t *testing.T) {
	for _, change := range []string{"option", "wrong-parent", "replacement", "new-consumer", "switch-parent", "removed"} {
		t.Run(change, func(t *testing.T) {
			nb, raw := ordinaryNetworkFixture(t, 23)
			guarded, err := nb.GuardNetworkNICReplayParentChange(context.Background(), 17, "incus-net23-lr-lrp-int-net17", "incus-net41-lr-lrp-int-net17", nil, nil)
			require.NoError(t, err)
			ordinaryForeignChange(t, raw, change)
			want := referenceContents(t, raw)
			require.Error(t, ordinaryACLUpdate(guarded))
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

func TestOrdinaryParentChangeRequiresOriginalRealBackend(t *testing.T) {
	for _, change := range []string{"removed", "duplicate-switch"} {
		t.Run(change, func(t *testing.T) {
			nb, raw := ordinaryNetworkFixture(t, 23)
			if change == "removed" {
				ordinaryForeignChange(t, raw, change)
			} else {
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net17-ls-int"}})
			}

			want := referenceContents(t, raw)
			_, err := nb.GuardNetworkNICReplayParentChange(context.Background(), 17, "incus-net23-lr-lrp-int-net17", "incus-net41-lr-lrp-int-net17", nil, nil)
			require.Error(t, err, "an empty or ambiguous initial set cannot authorize a parent transition")
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

func TestOrdinaryACLInfrastructureRealBackend(t *testing.T) {
	for _, parent := range []int64{0, 23} {
		t.Run(fmt.Sprint(parent), func(t *testing.T) {
			nb, raw := ordinaryNetworkFixture(t, parent)
			_, err := nb.GuardACLReferenceUpdate(context.Background(), 1, 29, nil)
			require.ErrorContains(t, err, "consumer is absent from current candidates", "the original NIC-only caller fails after the last NIC is deleted")
			candidate := NetworkReferenceInfrastructure{ProjectID: 1, NetworkID: 17, ParentID: parent}
			guarded, err := nb.GuardACLReferenceUpdate(context.Background(), 1, 29, nil, candidate)
			require.NoError(t, err)
			before := referenceContents(t, raw)
			require.NoError(t, ordinaryACLUpdate(guarded))
			after := referenceContents(t, raw)
			for _, table := range []string{"Logical_Switch", "Logical_Switch_Port", "Logical_Router", "Logical_Router_Policy", "Address_Set"} {
				require.ElementsMatch(t, before[table], after[table], table)
			}

			for _, row := range before["Port_Group"] {
				if row["name"] == "foreign" || row["name"] == "incus_acl31_net19" {
					require.Contains(t, after["Port_Group"], row, "sibling and foreign groups are preserved")
				}
			}
		})
	}
}

func TestOrdinaryACLInfrastructureRefusalsRealBackend(t *testing.T) {
	for _, change := range []string{"project", "network", "parent", "option", "replacement", "new-consumer", "switch-parent"} {
		t.Run(change, func(t *testing.T) {
			nb, raw := ordinaryNetworkFixture(t, 23)
			candidate := NetworkReferenceInfrastructure{ProjectID: 1, NetworkID: 17, ParentID: 23}
			switch change {
			case "project":
				candidate.ProjectID = 2
			case "network":
				candidate.NetworkID = 18
			case "parent":
				candidate.ParentID = 24
			}

			guarded, err := nb.GuardACLReferenceUpdate(context.Background(), 1, 29, nil, candidate)
			if change == "project" || change == "network" || change == "parent" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			ordinaryForeignChange(t, raw, change)
			want := referenceContents(t, raw)
			require.Error(t, ordinaryACLUpdate(guarded))
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}

// ordinaryEffectClient moves the real row after the snapshot and before its atomic wait.
type ordinaryEffectClient struct {
	ovsdbClient.Client
	raw    ovsdbClient.Client
	before func()
}

func (c *ordinaryEffectClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range ops {
		if op.Op != ovsdb.OperationWait || c.before == nil {
			continue
		}

		before := c.before
		c.before = nil
		before()
		break
	}

	result, err := c.raw.Transact(ctx, ops...)
	if err != nil {
		return nil, err
	}

	operationErrors, err := ovsdb.CheckOperationResults(result, ops)
	if err != nil {
		failures := []error{err}
		for _, failure := range operationErrors {
			failures = append(failures, failure)
		}

		return nil, errors.Join(failures...)
	}

	return result, nil
}

func TestOrdinaryNetworkUpdateAtomicRefusalRealBackend(t *testing.T) {
	for _, operation := range []string{"parent", "acl"} {
		t.Run(operation, func(t *testing.T) {
			nb, raw := ordinaryNetworkFixture(t, 23)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var guarded *NB
			var err error
			if operation == "parent" {
				guarded, err = nb.GuardNetworkNICReplayParentChange(ctx, 17, "incus-net23-lr-lrp-int-net17", "incus-net41-lr-lrp-int-net17", nil, nil)
			} else {
				guarded, err = nb.GuardACLReferenceUpdate(ctx, 1, 29, nil, NetworkReferenceInfrastructure{ProjectID: 1, NetworkID: 17, ParentID: 23})
			}

			require.NoError(t, err)
			var want map[string][]ovsdb.Row
			client, ok := guarded.client.(*referenceMutationClient)
			require.True(t, ok)
			effect := &ordinaryEffectClient{Client: client.Client, raw: raw, before: func() {
				ordinaryForeignChange(t, raw, "option")
				want = referenceContents(t, raw)
			}}
			client.Client = effect
			err = guarded.UpdatePortGroupACLRules(ctx, "incus_acl29_net17", nil, OVNACLRule{Direction: "to-lport", Action: "drop", Match: "ip4", Priority: 123})
			require.Nil(t, effect.before, "the real guarded transaction reached the effect boundary")
			require.True(t, PhysicalGuardMoved(err), "native atomic wait rejection: %T %v", err, err)
			referenceSameContents(t, want, referenceContents(t, raw))
		})
	}
}
