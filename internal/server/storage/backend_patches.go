package storage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

var earlyPatches = map[string]func(b *backend) error{
	"storage_missing_snapshot_records":         patchMissingSnapshotRecords,
	"storage_delete_old_snapshot_records":      patchDeleteOldSnapshotRecords,
	"storage_prefix_bucket_names_with_project": patchBucketNames,
}

var latePatches = map[string]func(b *backend) error{}

// Patches start here.

// patchMissingSnapshotRecords creates any missing storage volume records for instance volume snapshots.
// This is needed because it seems that in 2019 some instance snapshots did not have their associated volume DB
// records created. This later caused problems when we started validating that the instance snapshot DB record
// count matched the volume snapshot DB record count.
func patchMissingSnapshotRecords(b *backend) error {
	var err error
	var localNode string

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localNode, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get local member name: %w", err)
		}

		return err
	})
	if err != nil {
		return err
	}

	// Get instances on this local server (as the DB helper functions return volumes on local server), also
	// avoids running the same queries on every cluster member for instances on shared storage.
	filter := cluster.InstanceFilter{Node: &localNode}
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			// Check we can convert the instance to the volume type needed.
			volType, err := InstanceTypeToVolumeType(inst.Type)
			if err != nil {
				return err
			}

			contentType := drivers.ContentTypeFS
			if inst.Type == instancetype.VM {
				contentType = drivers.ContentTypeBlock
			}

			// Get all the instance snapshot DB records.
			var instPoolName string
			var snapshots []cluster.Instance
			err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				instPoolName, err = tx.GetInstancePool(ctx, p.Name, inst.Name)
				if err != nil {
					if api.StatusErrorCheck(err, http.StatusNotFound) {
						// If the instance cannot be associated to a pool its got bigger problems
						// outside the scope of this patch. Will skip due to empty instPoolName.
						return nil
					}

					return fmt.Errorf("Failed finding pool for instance %q in project %q: %w", inst.Name, p.Name, err)
				}

				snapshots, err = tx.GetInstanceSnapshotsWithName(ctx, p.Name, inst.Name)
				if err != nil {
					return err
				}

				return nil
			})
			if err != nil {
				return err
			}

			if instPoolName != b.Name() {
				return nil // This instance isn't hosted on this storage pool, skip.
			}

			dbVol, err := VolumeDBGet(b, p.Name, inst.Name, volType)
			if err != nil {
				return fmt.Errorf("Failed loading storage volume record %q: %w", inst.Name, err)
			}

			// Get all the instance volume snapshot DB records.
			dbVolSnaps, err := VolumeDBSnapshotsGet(b, p.Name, inst.Name, volType)
			if err != nil {
				return fmt.Errorf("Failed loading storage volume snapshot records %q: %w", inst.Name, err)
			}

			for i := range snapshots {
				foundVolumeSnapshot := false
				for _, dbVolSnap := range dbVolSnaps {
					if dbVolSnap.Name == snapshots[i].Name {
						foundVolumeSnapshot = true
						break
					}
				}

				if !foundVolumeSnapshot {
					b.logger.Info("Creating missing volume snapshot record", logger.Ctx{"project": snapshots[i].Project, "instance": snapshots[i].Name})
					err = VolumeDBCreate(b, snapshots[i].Project, snapshots[i].Name, "Auto repaired", volType, true, dbVol.Config, snapshots[i].CreationDate, time.Time{}, contentType, false, true)
					if err != nil {
						return err
					}
				}
			}

			return nil
		}, filter)
	})
	if err != nil {
		return err
	}

	return nil
}

// patchDeleteOldSnapshotRecords deletes the remaining snapshot records in storage_volumes
// (a previous patch would have already moved them into storage_volume_snapshots).
func patchDeleteOldSnapshotRecords(b *backend) error {
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		nodeID := tx.GetNodeID()
		_, err := tx.Tx().Exec(`
DELETE FROM storage_volumes WHERE id IN (
	SELECT id FROM storage_volumes
	JOIN (
		/* Create a two column intermediary table containing the container name and its snapshot name */
		SELECT sv.name inst_name, svs.name snap_name FROM storage_volumes AS sv
		JOIN storage_volumes_snapshots AS svs ON sv.id = svs.storage_volume_id AND node_id=? AND type=?
	) j1 ON name=printf("%s/%s", j1.inst_name, j1.snap_name)
	/* Only keep the records with a matching 'name' pattern, 'node_id' and 'type' */
);
`, nodeID, db.StoragePoolVolumeTypeContainer)
		if err != nil {
			return fmt.Errorf("Failed to delete remaining instance snapshot records in the `storage_volumes` table: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// patchBucketNames modifies the naming convention of bucket volumes by adding
// the corresponding project name as a prefix.
func patchBucketNames(b *backend) error {
	// Apply patch only for btrfs, dir, lvm, and zfs drivers.
	if !util.ValueInSlice(b.driver.Info().Name, []string{"btrfs", "dir", "lvm", "zfs"}) {
		return nil
	}

	var buckets map[string]*db.StorageBucket

	err := b.state.DB.Cluster.Transaction(b.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get local storage buckets.
		localBuckets, err := tx.GetStoragePoolBuckets(ctx, true)
		if err != nil {
			return err
		}

		buckets = make(map[string]*db.StorageBucket, len(localBuckets))
		for _, bucket := range localBuckets {
			buckets[bucket.Name] = bucket
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Get list of volumes.
	volumes, err := b.driver.ListVolumes()
	if err != nil {
		return err
	}

	for _, v := range volumes {
		// Ensure volume is of type bucket.
		if v.Type() != drivers.VolumeTypeBucket {
			continue
		}

		// Retrieve the bucket associated with the current volume's name.
		bucket, ok := buckets[v.Name()]
		if !ok {
			continue
		}

		newVolumeName := project.StorageVolume(bucket.Project, bucket.Name)

		// Rename volume.
		err := b.driver.RenameVolume(v, newVolumeName, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
