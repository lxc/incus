package acl

import (
	"context"
	"errors"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
)

func (d *common) physicalReferenceClient(update bool) (*ovn.NB, error) {
	var never bool
	var projectID int64
	var targets map[ovn.OVNSwitchPort]ovn.NICConfigPublication
	var infrastructure []ovn.NetworkReferenceInfrastructure
	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetNetworkACLID(ctx, tx.Tx(), d.projectName, d.info.Name)
		if err != nil {
			return err
		}

		if id != d.id || id <= 0 {
			return errors.New("Physical ACL catalog identity changed")
		}

		projectID, err = cluster.GetProjectID(ctx, tx.Tx(), d.projectName)
		if err != nil {
			return err
		}

		never, err = tx.OVNReferencesInapplicable(ctx)
		if err != nil {
			return err
		}

		if update && !never {
			targets, err = project.OVNNICReplayTargets(ctx, tx, d.projectName, 0)
			if err != nil {
				return err
			}

			networks, err := tx.GetCreatedNetworksByProject(ctx, d.projectName)
			if err != nil {
				return err
			}

			for id, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				candidate := ovn.NetworkReferenceInfrastructure{ProjectID: projectID, NetworkID: id}
				parent := network.Config["parent"]
				if parent != "" {
					for parentID, parentNetwork := range networks {
						if parentNetwork.Name == parent && parentNetwork.Type == "ovn" && parentNetwork.Config["parent"] == "" {
							candidate.ParentID = parentID
						}
					}

					if candidate.ParentID == 0 {
						return fmt.Errorf("Current ACL infrastructure network %q has no created parent", network.Name)
					}
				}

				infrastructure = append(infrastructure, candidate)
			}
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	if never {
		return nil, nil
	}

	if d.state.OVN == nil {
		return nil, errors.New("Physical reference mutation requires the original OVN backend")
	}

	nb, _, err := d.state.OVN()
	if err != nil {
		return nil, err
	}

	if update {
		return nb.GuardACLReferenceUpdate(context.TODO(), projectID, d.id, targets, infrastructure...)
	}

	return nb, nb.CheckACLPhysicalUnused(context.TODO(), projectID, d.id)
}

func physicalPublicationRoot(nb *ovn.NB) string {
	if nb == nil {
		return ""
	}

	return nb.BackendID()
}

// New resources still participate in the same durable activation/publication ordering.
func physicalCreationRoot(s *state.State) (string, error) {
	var never bool
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		never, err = tx.OVNReferencesInapplicable(ctx)
		return err
	})
	if err != nil {
		return "", err
	}

	if never {
		return "", nil
	}

	if s.OVN == nil {
		return "", errors.New("Resource creation requires original OVN applicability")
	}

	nb, _, err := s.OVN()
	if err != nil {
		return "", err
	}

	if nb == nil {
		return "", errors.New("Original OVN backend is unavailable")
	}

	return nb.BackendID(), nil
}
