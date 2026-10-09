//go:build linux && cgo && !agent

package addressset

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/project"
)

type addressSetGCMode string

const (
	addressSetGCOne  addressSetGCMode = "one"
	addressSetGCACLs addressSetGCMode = "acls"
	addressSetGCAll  addressSetGCMode = "all"
)

// addressSetGCRequest has exactly one bounded candidate source. ACL names are
// filters only: they neither exclude owners nor imply application authority.
type addressSetGCRequest struct {
	Mode     addressSetGCMode
	SetName  string
	ACLNames []string
}

type addressSetGCObject struct {
	Key  project.AddressSetUsageKey
	Name string
}

// selectUnusedAddressSets is the single transaction-owning selection path for
// all three collection wrappers. It resolves all names and dependencies before
// returning any exact-ID plan. Missing explicit names deliberately return errors.
// The snapshot is not durable backend deletion authority or caller atomicity.
func selectUnusedAddressSets(ctx context.Context, c *db.Cluster, projectName string, request addressSetGCRequest) ([]addressSetGCObject, error) {
	if ctx == nil || c == nil || projectName == "" {
		return nil, fmt.Errorf("Invalid address set collection context or project")
	}

	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	switch request.Mode {
	case addressSetGCOne:
		if request.SetName == "" || request.ACLNames != nil {
			return nil, fmt.Errorf("Invalid single address set request")
		}

	case addressSetGCACLs:
		if request.SetName != "" {
			return nil, fmt.Errorf("Invalid ACL address set request")
		}

	case addressSetGCAll:
		if request.SetName != "" || request.ACLNames != nil {
			return nil, fmt.Errorf("Invalid all address sets request")
		}

	default:
		return nil, fmt.Errorf("Unknown address set collection mode %q", request.Mode)
	}

	plan := []addressSetGCObject{}
	err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed resolving address set project %q: %w", projectName, err)
		}

		p, err := project.ReadNetworkAddressSetProtection(ctx, tx, projectID)
		if err != nil {
			return err
		}

		if p.ACLProtection.ProjectName != projectName {
			return fmt.Errorf("Address set resource project identity changed")
		}

		candidates := map[project.AddressSetUsageKey]bool{}
		switch request.Mode {
		case addressSetGCOne:
			for key, set := range p.Sets {
				if set.Name == request.SetName {
					candidates[key] = true
				}
			}
			if len(candidates) != 1 {
				return fmt.Errorf("Unknown requested address set %q in project %q", request.SetName, projectName)
			}

		case addressSetGCACLs:
			names := map[string]project.ACLUsageKey{}
			for key, acl := range p.ACLProtection.ACLs {
				names[acl.Name] = key
			}

			for _, name := range request.ACLNames {
				key, ok := names[name]
				if !ok {
					return fmt.Errorf("Unknown requested ACL %q in project %q", name, projectName)
				}

				for _, edge := range p.Edges {
					if edge.Source == key {
						candidates[edge.Target] = true
					}
				}
			}

		case addressSetGCAll:
			for key := range p.Sets {
				candidates[key] = true
			}
		}
		for _, edge := range p.Protected {
			delete(candidates, edge.Target)
		}
		// Cluster transactions may retry their callback. Never mix snapshots.
		plan = make([]addressSetGCObject, 0, len(candidates))
		for key := range candidates {
			plan = append(plan, addressSetGCObject{Key: key, Name: p.Sets[key].Name})
		}

		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(plan, func(a, b addressSetGCObject) int { return cmp.Compare(a.Key.AddressSetID, b.Key.AddressSetID) })
	return plan, nil
}
