//go:build linux && cgo && !agent

package project

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/util"
)

// AddressSetUsageKey identifies an address set in its exact resource project.
type AddressSetUsageKey struct {
	ProjectID    int64
	AddressSetID int64
}

// ACLAddressSetEdge preserves each persisted rule reference, including duplicates and disabled rules.
type ACLAddressSetEdge struct {
	Source    ACLUsageKey
	Target    AddressSetUsageKey
	Direction string
	Field     string
	RuleIndex int
	State     string
}

// NetworkAddressSetProtection is conservative ownership, not evidence of applied rules.
// ACLProtection preserves every owner and its provenance; Edges retain all rule occurrences.
type NetworkAddressSetProtection struct {
	ACLProtection *NetworkACLProtection
	Sets          map[AddressSetUsageKey]cluster.NetworkAddressSet
	Edges         []ACLAddressSetEdge
	Protected     []ACLAddressSetEdge
}

// ReadNetworkAddressSetProtection reads exact set dependencies in the caller's transaction.
// Set collectors have no current-owner exclusion or keep roots. Retained NIC owners
// protect current persisted rules; historical applied rule versions are not reconstructed.
func ReadNetworkAddressSetProtection(ctx context.Context, tx *db.ClusterTx, projectID int64) (*NetworkAddressSetProtection, error) {
	acls, err := ReadNetworkACLProtection(ctx, tx, projectID, ACLCurrentExclusion{})
	if err != nil {
		return nil, err
	}

	p := &NetworkAddressSetProtection{ACLProtection: acls, Sets: map[AddressSetUsageKey]cluster.NetworkAddressSet{}}
	sets, err := cluster.GetNetworkAddressSets(ctx, tx.Tx(), cluster.NetworkAddressSetFilter{Project: &acls.ProjectName})
	if err != nil {
		return nil, err
	}

	names := map[string]AddressSetUsageKey{}
	for _, set := range sets {
		id, err := cluster.GetNetworkAddressSetID(ctx, tx.Tx(), acls.ProjectName, set.Name)
		if err != nil {
			return nil, err
		}

		_, duplicate := names[set.Name]
		if id <= 0 || id != int64(set.ID) || int64(set.ProjectID) != projectID || set.Project != acls.ProjectName || set.Name == "" || duplicate {
			return nil, fmt.Errorf("Invalid address set catalog identity for %q", set.Name)
		}

		key := AddressSetUsageKey{ProjectID: projectID, AddressSetID: id}
		names[set.Name] = key
		p.Sets[key] = set
	}
	// Stable iteration preserves rule ordering without coalescing duplicate references.
	keys := make([]ACLUsageKey, 0, len(acls.ACLs))
	for key := range acls.ACLs {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b ACLUsageKey) int { return cmp.Compare(a.ACLID, b.ACLID) })
	for _, source := range keys {
		acl := acls.ACLs[source]
		for _, direction := range []string{"ingress", "egress"} {
			rules := acl.Ingress
			if direction == "egress" {
				rules = acl.Egress
			}

			for index, rule := range rules {
				for _, field := range []string{"Source", "Destination"} {
					value := rule.Source
					if field == "Destination" {
						value = rule.Destination
					}

					for _, token := range util.SplitNTrimSpace(value, ",", -1, true) {
						if !strings.HasPrefix(token, "$") {
							continue
						}

						target, ok := names[strings.TrimPrefix(token, "$")]
						if !ok || target.ProjectID != source.ProjectID {
							return nil, fmt.Errorf("Unknown address set rule reference %q in ACL %q (%s %s rule %d)", token, acl.Name, direction, field, index)
						}

						p.Edges = append(p.Edges, ACLAddressSetEdge{Source: source, Target: target, Direction: direction, Field: field, RuleIndex: index, State: rule.State})
					}
				}
			}
		}
	}
	// Subject-only ACLs never become direct roots, even through a cycle.
	direct := map[ACLUsageKey]bool{}
	for _, root := range acls.NetworkRoots {
		if acls.Networks[root.Network].Type == "ovn" {
			direct[root.ACL] = true
		}
	}
	addOwner := func(r db.ProfileReferenceResource) {
		if r.NetworkType == "ovn" && r.ACLID > 0 {
			direct[ACLUsageKey{ProjectID: r.ACLProjectID, ACLID: r.ACLID}] = true
		}
	}
	for _, owner := range acls.Owners.CurrentInstances {
		addOwner(owner.ProfileReferenceResource)
	}

	for _, owner := range acls.Owners.CurrentProfiles {
		addOwner(owner.ProfileReferenceResource)
	}

	for _, owner := range acls.Owners.Retained {
		addOwner(owner.ProfileReferenceResource)
	}

	for _, edge := range p.Edges {
		if direct[edge.Source] {
			p.Protected = append(p.Protected, edge)
		}
	}
	return p, nil
}
