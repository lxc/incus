//go:build linux && cgo && !agent

package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/shared/api"
)

// ProfileReferenceChildNetwork is an exact persisted network identity and its reserved child token.
// It authorizes no device effects by itself; callers must retain the admitted attempt's ownership.
type ProfileReferenceChildNetwork struct {
	NetworkID   int64
	ProjectID   int64
	ProjectName string
	Name        string
	Token       string
}

type profileReferenceChildNetwork struct {
	ProfileReferenceChildNetwork
	target bool
	state  NetworkState
}

// profileReferenceChildNetworks validates all saved identities before filtering or deduplicating.
// It deliberately does not resolve names through today's project settings or require local readiness.
func (c *ClusterTx) profileReferenceChildNetworks(ctx context.Context, identity ProfileReferenceApply, before, after ProfileReferenceSnapshot) ([]profileReferenceChildNetwork, error) {
	if identity.InstanceID <= 0 || identity.ProjectID <= 0 || identity.MemberID <= 0 || identity.PlacementRevision <= 0 || identity.Sequence <= 0 || identity.Token == "" || identity.Owner == "" {
		return nil, profileReferenceConflict()
	}

	type networkIdentity struct {
		id          int64
		projectID   int64
		projectName string
		name        string
		kind        string
	}

	type networkName struct {
		projectID int64
		name      string
	}

	identities := map[int64]networkIdentity{}
	names := map[networkName]int64{}
	targets := map[int64]bool{}
	for i, snapshot := range []ProfileReferenceSnapshot{before, after} {
		if snapshot.Version != 1 || snapshot.InstanceID != identity.InstanceID || snapshot.ProjectID != identity.ProjectID || snapshot.MemberID != identity.MemberID || snapshot.Project.Name == "" {
			return nil, profileReferenceConflict()
		}

		var projectExists bool
		err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id=? AND name=?)`, snapshot.ProjectID, snapshot.Project.Name).Scan(&projectExists)
		if err != nil {
			return nil, err
		}

		if !projectExists {
			return nil, profileReferenceConflict()
		}

		for _, resource := range snapshot.Resources {
			if resource.NetworkID <= 0 || resource.NetworkProjectID <= 0 || resource.NetworkProject == "" || resource.NetworkName == "" || resource.NetworkType == "" || resource.Device == "" {
				return nil, profileReferenceConflict()
			}

			recorded := networkIdentity{resource.NetworkID, resource.NetworkProjectID, resource.NetworkProject, resource.NetworkName, resource.NetworkType}
			previous, exists := identities[resource.NetworkID]
			if exists && previous != recorded {
				return nil, profileReferenceConflict()
			}

			name := networkName{resource.NetworkProjectID, resource.NetworkName}
			previousID, exists := names[name]
			if exists && previousID != resource.NetworkID {
				return nil, profileReferenceConflict()
			}

			identities[resource.NetworkID] = recorded
			names[name] = resource.NetworkID
			if i == 1 {
				targets[resource.NetworkID] = true
			}
		}
	}

	ids := make([]int64, 0, len(identities))
	for id := range identities {
		ids = append(ids, id)
	}

	slices.Sort(ids)
	plan := make([]profileReferenceChildNetwork, 0, len(ids))
	for _, id := range ids {
		recorded := identities[id]
		var projectID int64
		var projectName, name string
		var kind NetworkType
		var state NetworkState
		err := c.tx.QueryRowContext(ctx, `SELECT n.project_id, p.name, n.name, n.type, n.state FROM networks n JOIN projects p ON p.id=n.project_id WHERE n.id=?`, id).Scan(&projectID, &projectName, &name, &kind, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, profileReferenceConflict()
		}

		if err != nil {
			return nil, err
		}

		var network api.Network
		networkFillType(&network, kind)
		if recorded.projectID != projectID || recorded.projectName != projectName || recorded.name != name || recorded.kind != network.Type {
			return nil, profileReferenceConflict()
		}

		if recorded.kind == "ovn" {
			plan = append(plan, profileReferenceChildNetwork{ProfileReferenceChildNetwork: ProfileReferenceChildNetwork{NetworkID: id, ProjectID: projectID, ProjectName: projectName, Name: name}, target: targets[id], state: state})
		}
	}
	return plan, nil
}

// ReserveProfileReferenceChildren acquires and seals the full persisted baseline/target OVN union.
// It is an unwired prerequisite: target admission here checks global Created, not member readiness.
// Acquisition and sealing share Transaction's callback rollback. Returned errors expose no tokens;
// commit ambiguity, failed rollback and ErrTxDone normalization retain the inherited semantics.
func (c *Cluster) ReserveProfileReferenceChildren(ctx context.Context, identity ProfileReferenceApply) ([]ProfileReferenceChildNetwork, error) {
	var result []ProfileReferenceChildNetwork
	err := c.Transaction(ctx, func(ctx context.Context, tx *ClusterTx) error {
		// Transaction may invoke this callback again. Never carry tokens or results into a retry.
		result = nil
		phase, sealed, before, after, err := tx.profileReferenceAttempt(ctx, identity)
		if err != nil {
			return err
		}

		if phase != "applying" || sealed {
			return profileReferenceConflict()
		}

		plan, err := tx.profileReferenceChildNetworks(ctx, identity, before, after)
		if err != nil {
			return err
		}

		for _, network := range plan {
			if network.target && network.state != networkCreated {
				return profileReferenceConflict()
			}
		}

		reserved := make([]ProfileReferenceChildNetwork, 0, len(plan))
		children := make([]ProfileReferenceChild, 0, len(plan))
		tokens := map[string]struct{}{identity.Token: {}}
		for _, network := range plan {
			child := network.ProfileReferenceChildNetwork
			child.Token = uuid.NewString()
			_, exists := tokens[child.Token]
			if exists || child.Token == "" {
				return profileReferenceConflict()
			}

			tokens[child.Token] = struct{}{}
			err = tx.AcquireOVNNetworkOperation(ctx, child.ProjectName, child.Name, child.Token, "nic")
			if err != nil {
				return err
			}

			reserved = append(reserved, child)
			children = append(children, ProfileReferenceChild{NetworkID: child.NetworkID, ProjectID: child.ProjectID, Name: child.Name, Token: child.Token})
		}

		err = tx.SealProfileReferenceChildren(ctx, identity, children)
		if err != nil {
			return err
		}

		result = reserved
		return nil
	})
	if err != nil {
		return nil, err
	}

	return slices.Clone(result), nil
}

func profileReferenceChildOrder(a, b ProfileReferenceChild) int {
	return cmp.Compare(a.NetworkID, b.NetworkID)
}
