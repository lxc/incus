package storage

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/state"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// InstancePath returns the directory of an instance or snapshot.
func InstancePath(instanceType instancetype.Type, projectName, instanceName string, isSnapshot bool) string {
	fullName := project.Instance(projectName, instanceName)
	if instanceType == instancetype.VM {
		if isSnapshot {
			return internalUtil.VarPath("virtual-machines-snapshots", fullName)
		}

		return internalUtil.VarPath("virtual-machines", fullName)
	}

	if isSnapshot {
		return internalUtil.VarPath("containers-snapshots", fullName)
	}

	return internalUtil.VarPath("containers", fullName)
}

// InstanceImportingFilePath returns the file path used to indicate an instance import is in progress.
// This marker file is created when using `incusd import` to import an instance that exists on the storage device
// but does not exist in the Incus database. The presence of this file causes the instance not to be removed from
// the storage device if the import should fail for some reason.
func InstanceImportingFilePath(instanceType instancetype.Type, poolName, projectName, instanceName string) string {
	fullName := project.Instance(projectName, instanceName)

	typeDir := "containers"
	if instanceType == instancetype.VM {
		typeDir = "virtual-machines"
	}

	return internalUtil.VarPath("storage-pools", poolName, typeDir, fullName, ".importing")
}

// GetStoragePoolMountPoint returns the mountpoint of the given pool.
// {INCUS_DIR}/storage-pools/<pool>
// Deprecated, use GetPoolMountPath in storage/drivers package.
func GetStoragePoolMountPoint(poolName string) string {
	return internalUtil.VarPath("storage-pools", poolName)
}

// GetSnapshotMountPoint returns the mountpoint of the given container snapshot.
// ${INCUS_DIR}/storage-pools/<pool>/containers-snapshots/<snapshot_name>.
func GetSnapshotMountPoint(projectName, poolName string, snapshotName string) string {
	return internalUtil.VarPath("storage-pools", poolName, "containers-snapshots", project.Instance(projectName, snapshotName))
}

// GetImageMountPoint returns the mountpoint of the given image.
// ${INCUS_DIR}/storage-pools/<pool>/images/<fingerprint>.
func GetImageMountPoint(poolName string, fingerprint string) string {
	return internalUtil.VarPath("storage-pools", poolName, "images", fingerprint)
}

// GetStoragePoolVolumeSnapshotMountPoint returns the mountpoint of the given pool volume snapshot.
// ${INCUS_DIR}/storage-pools/<pool>/custom-snapshots/<custom volume name>/<snapshot name>.
func GetStoragePoolVolumeSnapshotMountPoint(poolName string, snapshotName string) string {
	return internalUtil.VarPath("storage-pools", poolName, "custom-snapshots", snapshotName)
}

// CreateContainerMountpoint creates the provided container mountpoint and symlink.
func CreateContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
	mntPointSymlinkExist := util.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := util.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0711)
		if err != nil {
			return err
		}
	}

	err = os.Chmod(mountPoint, 0100)
	if err != nil {
		return err
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(mountPoint, mountPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateSnapshotMountpoint creates the provided container snapshot mountpoint
// and symlink.
func CreateSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	snapshotMntPointExists := util.PathExists(snapshotMountpoint)
	mntPointSymlinkExist := util.PathExists(snapshotsSymlink)

	if !snapshotMntPointExists {
		err := os.MkdirAll(snapshotMountpoint, 0711)
		if err != nil {
			return err
		}
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(snapshotsSymlinkTarget, snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

// UsedBy returns list of API resources using storage pool. Accepts firstOnly argument to indicate that only the
// first resource using network should be returned. This can help to quickly check if the storage pool is in use.
// If memberSpecific is true, then the search is restricted to volumes that belong to this member or belong to
// all members. The ignoreVolumeType argument can be used to exclude certain volume type(s) from the list.
func UsedBy(ctx context.Context, s *state.State, pool Pool, firstOnly bool, memberSpecific bool, ignoreVolumeType ...string) ([]string, error) {
	var err error
	var usedBy []string

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all the volumes using the storage pool.
		volumes, err := tx.GetStoragePoolVolumes(ctx, pool.ID(), memberSpecific)
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		for _, vol := range volumes {
			var u *api.URL

			if slices.Contains(ignoreVolumeType, vol.Type) {
				continue
			}

			// Generate URL for volume based on types that map to other entities.
			if vol.Type == db.StoragePoolVolumeTypeNameContainer || vol.Type == db.StoragePoolVolumeTypeNameVM {
				volName, snapName, isSnap := api.GetParentAndSnapshotName(vol.Name)
				if isSnap {
					u = api.NewURL().Path(version.APIVersion, "instances", volName, "snapshots", snapName).Project(vol.Project)
				} else {
					u = api.NewURL().Path(version.APIVersion, "instances", volName).Project(vol.Project)
				}

				usedBy = append(usedBy, u.String())
			} else if vol.Type == db.StoragePoolVolumeTypeNameImage {
				imgProjectNames, err := tx.GetProjectsUsingImage(ctx, vol.Name)
				if err != nil {
					return fmt.Errorf("Failed loading projects using image %q: %w", vol.Name, err)
				}

				if len(imgProjectNames) > 0 {
					for _, imgProjectName := range imgProjectNames {
						u = api.NewURL().Path(version.APIVersion, "images", vol.Name).Project(imgProjectName).Target(vol.Location)
						usedBy = append(usedBy, u.String())
					}
				} else {
					// Handle orphaned image volumes that are not associated to an image.
					u = vol.URL(version.APIVersion, pool.Name())
					usedBy = append(usedBy, u.String())
				}
			} else {
				u = vol.URL(version.APIVersion, pool.Name())
				usedBy = append(usedBy, u.String())
			}

			if firstOnly {
				return nil
			}
		}

		// Get all buckets using the storage pool.
		poolID := pool.ID()
		filters := []db.StorageBucketFilter{{
			PoolID: &poolID,
		}}

		buckets, err := tx.GetStoragePoolBuckets(ctx, memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage buckets: %w", err)
		}

		for _, bucket := range buckets {
			u := bucket.URL(version.APIVersion, pool.Name(), bucket.Project)
			usedBy = append(usedBy, u.String())

			if firstOnly {
				return nil
			}
		}

		// Get all the profiles using the storage pool.
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading profiles: %w", err)
		}

		for _, profile := range profiles {
			profileDevices, err := cluster.GetProfileDevices(ctx, tx.Tx(), profile.ID)
			if err != nil {
				return fmt.Errorf("Failed loading profile devices: %w", err)
			}

			for _, device := range profileDevices {
				if device.Type != cluster.TypeDisk {
					continue
				}

				if device.Config["pool"] != pool.Name() {
					continue
				}

				u := api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(profile.Project)
				usedBy = append(usedBy, u.String())

				if firstOnly {
					return nil
				}

				break
			}
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(usedBy)

	return usedBy, nil
}
