package ovn

import (
	"context"
	"fmt"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NetworkPeerPolicy binds a constructor policy to a peer resolved by the deleting network's caller.
type NetworkPeerPolicy struct {
	RouterID int64
	PeerID   int64
}

func (s *physicalReferences) networkPeerPolicies(networkID int64, peers []NetworkPeerPolicy) (map[string]ovsdb.Row, error) {
	policies := map[string]ovsdb.Row{}
	for _, peer := range peers {
		if peer.RouterID <= 0 || peer.PeerID <= 0 || peer.RouterID == peer.PeerID || peer.RouterID == networkID || peer.PeerID == networkID {
			return nil, fmt.Errorf("Invalid child network peer policy identity")
		}

		routerName := fmt.Sprintf("incus-net%d-lr", peer.RouterID)
		router, err := retirementNamedRow(s, "Logical_Router", routerName)
		if err != nil {
			return nil, err
		}

		if router == nil {
			return nil, fmt.Errorf("%w: child network peer router is absent", ErrPhysicalReference)
		}

		members, err := physicalUUIDs(router["policies"])
		if err != nil {
			return nil, err
		}

		for _, family := range []string{"4", "6"} {
			matched := false
			match := fmt.Sprintf(`(inport == "%s-lrp-ext" && ip%s && ip%s.src == $incus_net%d_routes_ip%s) // %s-lrp-peer-net%d`, routerName, family, family, networkID, family, routerName, peer.PeerID)
			for _, row := range s.rows["Logical_Router_Policy"] {
				if row["match"] != match {
					continue
				}

				if matched {
					return nil, fmt.Errorf("%w: child network peer policy is ambiguous", ErrPhysicalReference)
				}

				matched = true
				id, err := nicCleanupRowUUID(row, "_uuid")
				if err != nil {
					return nil, err
				}

				if !members[id] || row["action"] != "drop" || row["priority"] != float64(500) {
					return nil, fmt.Errorf("%w: child network peer policy differs from its constructor", ErrPhysicalReference)
				}

				for _, column := range []string{"external_ids", "options"} {
					values, err := nicCleanupStringMap(row[column])
					if err != nil || len(values) != 0 {
						return nil, fmt.Errorf("%w: child network peer policy has foreign metadata", ErrPhysicalReference)
					}
				}

				for _, column := range []string{"nexthop", "nexthops", "bfd_sessions"} {
					values, ok := row[column].(ovsdb.OvsSet)
					if !ok || len(values.GoSet) != 0 {
						return nil, fmt.Errorf("%w: child network peer policy has foreign effects", ErrPhysicalReference)
					}
				}

				err = retirementParent(s, "Logical_Router", "policies", row, router)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrPhysicalReference, err)
				}

				policies[id] = row
			}
		}
	}

	return policies, nil
}

// DeleteNetworkPeerPolicies withdraws only exact peer anti-spoof rules for the deleting child's sets.
func (o *NB) DeleteNetworkPeerPolicies(ctx context.Context, networkID int64, routerPort string, peers ...NetworkPeerPolicy) error {
	if len(peers) == 0 {
		return nil
	}

	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}

	err = s.networkUnused(networkID, routerPort, peers...)
	if err != nil {
		return err
	}

	policies, err := s.networkPeerPolicies(networkID, peers)
	if err != nil {
		return err
	}

	ops := s.waits()
	for id := range policies {
		for _, router := range s.rows["Logical_Router"] {
			members, err := physicalUUIDs(router["policies"])
			if err != nil {
				return err
			}

			if members[id] {
				ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Router", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: router["_uuid"]}}, Mutations: []ovsdb.Mutation{{Column: "policies", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: id}}}}}})
			}
		}

		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Router_Policy", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}})
	}

	_, err = o.nicCleanupTransact(ctx, ops...)
	return err
}
