package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

// deleteInstanceRecord captures current NIC network identities in the transaction deleting their owner.
func (d *common) deleteInstanceRecord() ([]ovnCommittedACLCollection, error) {
	var collections []ovnCommittedACLCollection
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		collections = nil
		if !d.IsSnapshot() {
			current, err := cluster.GetInstance(ctx, tx.Tx(), d.project.Name, d.name)
			if err != nil {
				return err
			}

			if current.ID <= 0 || current.ID != d.id {
				return errors.New("Deleting instance database identity changed")
			}

			args, err := tx.InstancesToInstanceArgs(ctx, true, *current)
			if err != nil {
				return err
			}

			owner := args[current.ID]
			projectRecord, err := cluster.GetProject(ctx, tx.Tx(), d.project.Name)
			if err != nil {
				return err
			}

			projectInfo, err := projectRecord.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			seen := map[ovnDeviceNetwork]bool{}
			for _, dev := range db.ExpandInstanceDevices(owner.Devices, owner.Profiles) {
				if dev["type"] != "nic" || dev["network"] == "" {
					continue
				}

				key := ovnDeviceNetwork{project: project.NetworkProjectForNameFromRecord(projectInfo, dev["network"]), name: dev["network"]}
				if seen[key] {
					continue
				}

				seen[key] = true
				id, info, _, err := tx.GetNetworkInAnyState(ctx, key.project, key.name)
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					continue
				} else if err != nil {
					return err
				}

				if info.Type != "ovn" || info.Status != api.NetworkStatusCreated {
					continue
				}

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

				if owner.Config["volatile.uuid"] != d.localConfig["volatile.uuid"] {
					return errors.New("Deleting OVN instance UUID changed")
				}

				collections = append(collections, ovnCommittedACLCollection{projectName: key.project, networkName: key.name, projectID: projectID, networkID: id})
			}
		}

		return tx.DeleteInstance(ctx, d.project.Name, d.name)
	})
	if err != nil {
		return nil, err
	}

	return collections, nil
}

// collectDeletedInstanceOVNACLs runs after the original instance deletion and local cleanup commit.
func (d *common) collectDeletedInstanceOVNACLs(collections []ovnCommittedACLCollection) error {
	var result error
	for _, collection := range collections {
		err := network.OVNCollectNetworkACLGroups(d.state, collection.projectName, collection.networkName, collection.projectID, collection.networkID)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("Instance deletion committed; failed collecting unused ACLs for network %q (retry with a normal network update): %w", collection.networkName, err))
		}
	}

	return result
}
