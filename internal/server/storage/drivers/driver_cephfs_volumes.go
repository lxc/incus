package drivers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lxc/incus/internal/instancewriter"
	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/backup"
	localMigration "github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
)

// CreateVolume creates a new storage volume on disk.
func (d *cephfs) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	if vol.volType != VolumeTypeCustom {
		return ErrNotSupported
	}

	if vol.contentType != ContentTypeFS {
		return ErrNotSupported
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Setup for revert.
	revertPath := true
	defer func() {
		if revertPath {
			_ = os.RemoveAll(volPath)
		}
	}()

	// Apply the volume quota if specified.
	err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
	if err != nil {
		return err
	}

	// Fill the volume.
	err = d.runFiller(vol, "", filler, false)
	if err != nil {
		return err
	}

	revertPath = false
	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *cephfs) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy copies an existing storage volume (with or without snapshots) into a new volume.
func (d *cephfs) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	bwlimit := d.config["rsync.bwlimit"]

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)

			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)
			_ = d.DeleteVolumeSnapshot(snapVol, op)
		}

		_ = os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if copySnapshots && !srcVol.IsSnapshot() {
			// Get the list of snapshots from the source.
			srcSnapshots, err := srcVol.Snapshots(op)
			if err != nil {
				return err
			}

			for _, srcSnapshot := range srcSnapshots {
				_, snapName, _ := api.GetParentAndSnapshotName(srcSnapshot.name)

				// Mount the source snapshot.
				err = srcSnapshot.MountTask(func(srcMountPath string, op *operations.Operation) error {
					// Copy the snapshot.
					_, err = rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
					return err
				}, op)

				// Create the snapshot itself.
				err = d.CreateVolumeSnapshot(srcSnapshot, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				revertSnaps = append(revertSnaps, snapName)
			}
		}

		// Apply the volume quota if specified.
		err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
		if err != nil {
			return err
		}

		// Copy source to destination (mounting each volume if needed).
		err = srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, false)
			return err
		}, op)
		if err != nil {
			return err
		}

		// Run EnsureMountPath after mounting and copying to ensure the mounted directory has the
		// correct permissions set.
		return vol.EnsureMountPath()
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil // Don't revert.
	return nil
}

// CreateVolumeFromMigration creates a new volume (with or without snapshots) from a migration data stream.
func (d *cephfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs localMigration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

	// Create the main volume path.
	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	// Create slice of snapshots created if revert needed later.
	revertSnaps := []string{}
	defer func() {
		if revertSnaps == nil {
			return
		}

		// Remove any paths created if we are reverting.
		for _, snapName := range revertSnaps {
			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

			_ = d.DeleteVolumeSnapshot(snapVol, op)
		}

		_ = os.RemoveAll(volPath)
	}()

	// Ensure the volume is mounted.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		path := internalUtil.AddSlash(mountPath)

		// Snapshots are sent first by the sender, so create these first.
		for _, snapName := range volTargetArgs.Snapshots {
			// Receive the snapshot.
			var wrapper *ioprogress.ProgressTracker
			if volTargetArgs.TrackProgress {
				wrapper = localMigration.ProgressTracker(op, "fs_progress", snapName)
			}

			err = rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
			if err != nil {
				return err
			}

			fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
			snapVol := NewVolume(d, d.name, vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

			// Create the snapshot itself.
			err = d.CreateVolumeSnapshot(snapVol, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			revertSnaps = append(revertSnaps, snapName)
		}

		if vol.contentType == ContentTypeFS {
			// Apply the size limit.
			err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
			if err != nil {
				return err
			}
		}

		// Receive the main volume from sender.
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = localMigration.ProgressTracker(op, "fs_progress", vol.name)
		}

		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}, op)
	if err != nil {
		return err
	}

	revertSnaps = nil
	return nil
}

// DeleteVolume destroys the on-disk state of a volume.
func (d *cephfs) DeleteVolume(vol Volume, op *operations.Operation) error {
	snapshots, err := d.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volPath := GetVolumeMountPath(d.name, vol.volType, vol.name)

	// If the volume doesn't exist, then nothing more to do.
	if !util.PathExists(volPath) {
		return nil
	}

	// Remove the volume from the storage device.
	err = os.RemoveAll(volPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to delete '%s': %w", volPath, err)
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	err = os.RemoveAll(snapshotDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to delete '%s': %w", snapshotDir, err)
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *cephfs) HasVolume(vol Volume) (bool, error) {
	return genericVFSHasVolume(vol)
}

// ValidateVolume validates the supplied volume config. Optionally removes invalid keys from the volume's config.
func (d *cephfs) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// UpdateVolume applies the driver specific changes of a volume configuration change.
func (d *cephfs) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space usage of a volume.
func (d *cephfs) GetVolumeUsage(vol Volume) (int64, error) {
	// Snapshot usage not supported for CephFS.
	if vol.IsSnapshot() {
		return -1, ErrNotSupported
	}

	out, err := subprocess.RunCommand("getfattr", "-n", "ceph.quota.max_bytes", "--only-values", GetVolumeMountPath(d.name, vol.volType, vol.name))
	if err != nil {
		return -1, err
	}

	size, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return -1, err
	}

	return size, nil
}

// SetVolumeQuota applies a size limit on volume.
func (d *cephfs) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// If size not specified in volume config, then use pool's default volume.size setting.
	if size == "" || size == "0" {
		size = d.config["volume.size"]
	}

	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	_, err = subprocess.RunCommand("setfattr", "-n", "ceph.quota.max_bytes", "-v", fmt.Sprintf("%d", sizeBytes), GetVolumeMountPath(d.name, vol.volType, vol.name))
	return err
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *cephfs) GetVolumeDiskPath(vol Volume) (string, error) {
	return "", ErrNotSupported
}

// ListVolumes returns a list of volumes in storage pool.
func (d *cephfs) ListVolumes() ([]Volume, error) {
	return genericVFSListVolumes(d)
}

// MountVolume sets up the volume for use.
func (d *cephfs) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	return nil
}

// UnmountVolume clears any runtime state for the volume.
// As driver doesn't have volumes to unmount it returns false indicating the volume was already unmounted.
func (d *cephfs) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	refCount := vol.MountRefCountDecrement()
	if refCount > 0 {
		d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
		return false, ErrInUse
	}

	return false, nil
}

// RenameVolume renames the volume and all related filesystem entries.
func (d *cephfs) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, vol.volType, newVolName)
	if err != nil {
		return err
	}

	type volRevert struct {
		oldPath   string
		newPath   string
		isSymlink bool
	}

	// Create slice to record paths renamed if revert needed later.
	revertPaths := []volRevert{}
	defer func() {
		// Remove any paths rename if we are reverting.
		for _, vol := range revertPaths {
			if vol.isSymlink {
				_ = os.Symlink(vol.oldPath, vol.newPath)
			} else {
				_ = os.Rename(vol.newPath, vol.oldPath)
			}
		}

		// Remove the new snapshot directory if we are reverting.
		if len(revertPaths) > 0 {
			snapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)
			_ = os.RemoveAll(snapshotDir)
		}
	}()

	// Rename the snapshot directory first.
	srcSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)

	if util.PathExists(srcSnapshotDir) {
		targetSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)

		err = os.Rename(srcSnapshotDir, targetSnapshotDir)
		if err != nil {
			return fmt.Errorf("Failed to rename '%s' to '%s': %w", srcSnapshotDir, targetSnapshotDir, err)
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath: srcSnapshotDir,
			newPath: targetSnapshotDir,
		})
	}

	// Rename any snapshots of the volume too.
	snapshots, err := vol.Snapshots(op)
	if err != nil {
		return err
	}

	sourcePath := GetVolumeMountPath(d.name, vol.volType, newVolName)
	targetPath := GetVolumeMountPath(d.name, vol.volType, newVolName)

	for _, snapshot := range snapshots {
		// Figure out the snapshot paths.
		_, snapName, _ := api.GetParentAndSnapshotName(snapshot.name)
		oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapName)
		newCephSnapPath := filepath.Join(targetPath, ".snap", snapName)
		oldPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(vol.name, snapName))
		newPath := GetVolumeMountPath(d.name, vol.volType, GetSnapshotVolumeName(newVolName, snapName))

		// Update the symlink.
		err = os.Symlink(newCephSnapPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed to symlink '%s' to '%s': %w", newCephSnapPath, newPath, err)
		}

		revertPaths = append(revertPaths, volRevert{
			oldPath:   oldPath,
			newPath:   oldCephSnapPath,
			isSymlink: true,
		})
	}

	oldPath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	newPath := GetVolumeMountPath(d.name, vol.volType, newVolName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("Failed to rename '%s' to '%s': %w", oldPath, newPath, err)
	}

	revertPaths = append(revertPaths, volRevert{
		oldPath: oldPath,
		newPath: newPath,
	})

	revertPaths = nil
	return nil
}

// MigrateVolume streams the volume (with or without snapshots).
func (d *cephfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *localMigration.VolumeSourceArgs, op *operations.Operation) error {
	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume creates an exported version of a volume.
func (d *cephfs) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a new snapshot.
func (d *cephfs) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Create the snapshot.
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err := os.Mkdir(cephSnapPath, 0711)
	if err != nil {
		return fmt.Errorf("Failed to create directory '%s': %w", cephSnapPath, err)
	}

	// Create the parent directory.
	err = createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	// Create the symlink.
	targetPath := snapVol.MountPath()
	err = os.Symlink(cephSnapPath, targetPath)
	if err != nil {
		return fmt.Errorf("Failed to symlink '%s' to '%s': %w", cephSnapPath, targetPath, err)
	}

	return nil
}

// DeleteVolumeSnapshot deletes a snapshot.
func (d *cephfs) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Delete the snapshot itself.
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapName)

	err := os.Remove(cephSnapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", cephSnapPath, err)
	}

	// Remove the symlink.
	snapPath := snapVol.MountPath()
	err = os.Remove(snapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", snapPath, err)
	}

	return nil
}

// MountVolumeSnapshot makes the snapshot available for use.
func (d *cephfs) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	return nil
}

// UnmountVolumeSnapshot clears any runtime state for the snapshot.
func (d *cephfs) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	refCount := snapVol.MountRefCountDecrement()
	if refCount > 0 {
		d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
		return false, ErrInUse
	}

	return false, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *cephfs) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return genericVFSVolumeSnapshots(d, vol, op)
}

// RestoreVolume resets a volume to its snapshotted state.
func (d *cephfs) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	sourcePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
	cephSnapPath := filepath.Join(sourcePath, ".snap", snapshotName)

	// Restore using rsync.
	bwlimit := d.config["rsync.bwlimit"]
	output, err := rsync.LocalCopy(cephSnapPath, vol.MountPath(), bwlimit, false)
	if err != nil {
		return fmt.Errorf("Failed to rsync volume: %s: %w", string(output), err)
	}

	return nil
}

// RenameVolumeSnapshot renames a snapshot.
func (d *cephfs) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapVol.name)
	sourcePath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
	oldCephSnapPath := filepath.Join(sourcePath, ".snap", snapName)
	newCephSnapPath := filepath.Join(sourcePath, ".snap", newSnapshotName)

	err := os.Rename(oldCephSnapPath, newCephSnapPath)
	if err != nil {
		return fmt.Errorf("Failed to rename '%s' to '%s': %w", oldCephSnapPath, newCephSnapPath, err)
	}

	// Re-generate the snapshot symlink.
	oldPath := snapVol.MountPath()
	err = os.Remove(oldPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", oldPath, err)
	}

	newPath := GetVolumeMountPath(d.name, snapVol.volType, GetSnapshotVolumeName(parentName, newSnapshotName))
	err = os.Symlink(newCephSnapPath, newPath)
	if err != nil {
		return fmt.Errorf("Failed to symlink '%s' to '%s': %w", newCephSnapPath, newPath, err)
	}

	return nil
}
