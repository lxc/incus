package drivers

import (
	"context"
	"errors"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/shared/api"
)

type ovnCommittedACLCollection struct {
	projectName string
	networkName string
	projectID   int64
	networkID   int64
}

func (d *common) committedOVNACLCollections(committed bool) ([]ovnCommittedACLCollection, error) {
	if !committed || len(d.ovnDeviceUpdateOperations) == 0 {
		return nil, nil
	}

	var collections []ovnCommittedACLCollection
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		never, err := tx.OVNReferencesInapplicable(ctx)
		if err != nil || never {
			return err
		}

		for key, operation := range d.ovnDeviceUpdateOperations {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), key.project)
			if err != nil {
				return err
			}

			var hasACLs bool
			err = tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_acls WHERE project_id=?)`, projectID).Scan(&hasACLs)
			if err != nil {
				return err
			}

			if !hasACLs {
				continue
			}

			id, info, _, err := tx.GetNetworkInAnyState(ctx, key.project, key.name)
			if err != nil {
				return err
			}

			if operation.id <= 0 || id != operation.id || info.Type != "ovn" || info.Status != api.NetworkStatusCreated {
				return errors.New("Committed ACL collection lost its original network identity")
			}

			collections = append(collections, ovnCommittedACLCollection{projectName: key.project, networkName: key.name, projectID: projectID, networkID: id})
		}

		return nil
	})
	return collections, err
}

func (d *common) collectCommittedOVNACLs(collections []ovnCommittedACLCollection) error {
	var result error
	for _, collection := range collections {
		err := network.OVNCollectNetworkACLGroups(d.state, collection.projectName, collection.networkName, collection.projectID, collection.networkID)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("Instance configuration committed; failed collecting unused ACLs for network %q (retry with a normal network update): %w", collection.networkName, err))
		}
	}

	return result
}
