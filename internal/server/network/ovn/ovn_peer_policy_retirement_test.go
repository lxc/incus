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
)

func TestChildNetworkPeerPolicyRetirementRealBackend(t *testing.T) {
	for _, control := range []string{"supported", "wrong-action", "wrong-priority", "foreign-metadata", "shared-parent", "foreign-rule", "duplicate-policy", "wrong-peer", "changed-before-effect"} {
		t.Run(control, func(t *testing.T) {
			nb, raw, _ := retirementFixture(t, 23)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, nb.CreateLogicalRouter(ctx, "incus-net41-lr", false))
			policies := []OVNRouterPolicy{}
			for _, networkID := range []int64{17, 19, 23} {
				_, prefix, err := net.ParseCIDR(fmt.Sprintf("192.0.%d.0/24", networkID))
				require.NoError(t, err)
				require.NoError(t, nb.CreateAddressSet(ctx, OVNAddressSet(fmt.Sprintf("incus_net%d_routes", networkID)), *prefix))
				for _, family := range []string{"4", "6"} {
					policies = append(policies, OVNRouterPolicy{Priority: 500, Action: "drop", Match: fmt.Sprintf(`(inport == "incus-net41-lr-lrp-ext" && ip%s && ip%s.src == $incus_net%d_routes_ip%s) // incus-net41-lr-lrp-peer-net23`, family, family, networkID, family)})
				}
			}

			require.NoError(t, nb.UpdateLogicalRouterPolicy(ctx, "incus-net41-lr", policies...))
			initial := referenceContents(t, raw)
			var childID ovsdb.UUID
			for _, row := range initial["Logical_Router_Policy"] {
				if row["match"] == policies[0].Match {
					id, err := nicCleanupRowUUID(row, "_uuid")
					require.NoError(t, err)
					childID = ovsdb.UUID{GoUUID: id}
				}
			}

			require.NotEmpty(t, childID.GoUUID)
			where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: childID}}
			switch control {
			case "wrong-action":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router_Policy", Where: where, Row: ovsdb.Row{"action": "allow"}})
			case "wrong-priority":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router_Policy", Where: where, Row: ovsdb.Row{"priority": 501}})
			case "foreign-metadata":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router_Policy", Where: where, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{"owner": "foreign"})}})
			case "shared-parent":
				referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router", Row: ovsdb.Row{"name": "foreign", "policies": ovsdb.OvsSet{GoSet: []any{childID}}}})
			case "duplicate-policy":
				referenceExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Router_Policy", UUIDName: "duplicate_policy", Row: ovsdb.Row{"priority": 500, "action": "drop", "match": policies[0].Match}},
					ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Router", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net41-lr"}}, Mutations: []ovsdb.Mutation{{Column: "policies", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "duplicate_policy"}}}}}})
			case "foreign-rule":
				referenceExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "foreign_acl", Row: ovsdb.Row{"priority": 500, "action": "drop", "direction": "to-lport", "match": "$incus_net17_routes_ip4 == ip4.src"}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign_rule", "acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "foreign_acl"}}}}})
			}

			before := referenceContents(t, raw)
			peer := NetworkPeerPolicy{RouterID: 41, PeerID: 23}
			if control == "wrong-peer" {
				peer.PeerID = 24
			}

			port := "incus-net23-lr-lrp-int-net17"
			require.ErrorIs(t, nb.CheckNetworkPhysicalUnused(ctx, 17, port), ErrPhysicalReference, "original guard reproduces supported child-delete refusal")
			if control == "supported" || control == "changed-before-effect" {
				require.NoError(t, nb.CheckNetworkPhysicalUnused(ctx, 17, port, peer))
				client := nb.GuardNetworkDelete(17, port, peer)
				if control == "changed-before-effect" {
					// Use the unmonitored native connection to isolate the transaction wait from cache delivery.
					client = &NB{backendID: nb.backendID, client: &peerPolicyEffectClient{Client: raw, before: func() {
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Router_Policy", Where: where, Row: ovsdb.Row{"action": "allow"}})
						before = referenceContents(t, raw)
					}}}
					err := client.DeleteNetworkPeerPolicies(ctx, 17, port, peer)
					require.Error(t, err)
					require.True(t, PhysicalGuardMoved(err), "actual rejection: %T %v", err, err)
					referenceSameContents(t, before, referenceContents(t, raw))
					return
				}

				require.NoError(t, client.DeleteNetworkPeerPolicies(ctx, 17, port, peer))
				after := referenceContents(t, raw)
				removed := map[string]bool{}
				for _, row := range before["Logical_Router_Policy"] {
					if row["match"] == policies[0].Match || row["match"] == policies[1].Match {
						id, err := nicCleanupRowUUID(row, "_uuid")
						require.NoError(t, err)
						removed[id] = true
						referenceRemoveUUID(before, "Logical_Router_Policy", ovsdb.UUID{GoUUID: id})
					}
				}

				for _, row := range before["Logical_Router"] {
					if row["name"] == "incus-net41-lr" {
						for _, actual := range after["Logical_Router"] {
							if actual["_uuid"] == row["_uuid"] {
								row["_version"] = actual["_version"]
							}
						}
					}

					members, err := nicCleanupUUIDSet(row["policies"])
					require.NoError(t, err)
					kept := []any{}
					for _, id := range members {
						if !removed[id] {
							kept = append(kept, ovsdb.UUID{GoUUID: id})
						}
					}

					row["policies"] = ovsdb.OvsSet{GoSet: kept}
				}

				referenceSameContents(t, before, after)
				require.NoError(t, nb.CheckNetworkPhysicalUnused(ctx, 17, port))
				require.NoError(t, client.DeleteNetworkPeerPolicies(ctx, 17, port, peer), "retry after acknowledged withdrawal")
				require.NoError(t, client.DeleteLogicalSwitch(ctx, "incus-net17-ls-int"))
				require.NoError(t, client.DeleteAddressSet(ctx, "incus_net17_routes"))
				sets := referenceContents(t, raw)["Address_Set"]
				require.Len(t, sets, 4, "only parent and sibling family sets remain")
				for _, row := range sets {
					require.NotContains(t, row["name"], "incus_net17_routes")
				}

				t.Log("Owned child peer rules retired; sibling/parent rules and all unrelated tables preserved; internal switch deletion and retry succeed")
			} else {
				require.ErrorIs(t, nb.CheckNetworkPhysicalUnused(ctx, 17, port, peer), ErrPhysicalReference)
				require.ErrorIs(t, nb.DeleteNetworkPeerPolicies(ctx, 17, port, peer), ErrPhysicalReference)
				referenceSameContents(t, before, referenceContents(t, raw))
			}
		})
	}
}

// peerPolicyEffectClient preserves native operation errors without introducing a monitored client.
type peerPolicyEffectClient struct {
	ovsdbClient.Client
	before func()
}

func (c *peerPolicyEffectClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range ops {
		if op.Op == ovsdb.OperationDelete && c.before != nil {
			before := c.before
			c.before = nil
			before()
			break
		}
	}
	result, err := c.Client.Transact(ctx, ops...)
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
