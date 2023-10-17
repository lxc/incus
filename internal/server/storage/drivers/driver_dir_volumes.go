package drivers

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lxc/incus/internal/instancewriter"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/storage/quota"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *dir) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	volPath := vol.MountPath()

	revert := revert.New()
	defer revert.Fail()

	if util.PathExists(vol.MountPath()) {
		return fmt.Errorf("Volume path %q already exists", vol.MountPath())
	}

	// Create the volume itself.
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.RemoveAll(volPath) })

	// Get path to disk volume if volume is block or iso.
	rootBlockPath := ""
	if IsContentBlock(vol.contentType) {
		// We expect the filler to copy the VM image into this path.
		rootBlockPath, err = d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}
	} else if vol.volType != VolumeTypeBucket {
		// Filesystem quotas only used with non-block volume types.
		revertFunc, err := d.setupInitialQuota(vol)
		if err != nil {
			return err
		}

		if revertFunc != nil {
			revert.Add(revertFunc)
		}
	}

	// Run the volume filler function if supplied.
	err = d.runFiller(vol, rootBlockPath, filler, false)
	if err != nil {
		return err
	}

	// If we are creating a block volume, resize it to the requested size or the default.
	// For block volumes, we expect the filler function to have converted the qcow2 image to raw into the rootBlockPath.
	// For ISOs the content will just be copied.
	if IsContentBlock(vol.contentType) {
		// Convert to bytes.
		sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
		if err != nil {
			return err
		}

		// Ignore ErrCannotBeShrunk when setting size this just means the filler run above has needed to
		// increase the volume size beyond the default block volume size.
		_, err = ensureVolumeBlockFile(vol, rootBlockPath, sizeBytes, false)
		if err != nil && !errors.Is(err, ErrCannotBeShrunk) {
			return err
		}

		// Move the GPT alt header to end of disk if needed and if filler specified.
		if vol.IsVMBlock() && filler != nil && filler.Fill != nil {
			err = d.moveGPTAltHeader(rootBlockPath)
			if err != nil {
				return err
			}
		}
	}

	revert.Success()
	return nil
}

// CreateVolumeFromBackup restores a backup tarball onto the storage device.
func (d *dir) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	// Run the generic backup unpacker
	postHook, revertHook, err := genericVFSBackupUnpack(d.withoutGetVolID(), d.state.OS, vol, srcBackup.Snapshots, srcData, op)
	if err != nil {
		return nil, nil, err
	}

	// genericVFSBackupUnpack returns a nil postHook when volume's type is VolumeTypeCustom which
	// doesn't need any post hook processing after DB record creation.
	if postHook != nil {
		// Define a post hook function that can be run once the backup config has been restored.
		// This will setup the quota using the restored config.
		postHookWrapper := func(vol Volume) error {
			err := postHook(vol)
			if err != nil {
				return err
			}

			revert := revert.New()
			defer revert.Fail()

			revertQuota, err := d.setupInitialQuota(vol)
			if err != nil {
				return err
			}

			revert.Add(revertQuota)

			revert.Success()
			return nil
		}

		return postHookWrapper, revertHook, nil
	}

	return nil, revertHook, nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *dir) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	var err error
	var srcSnapshots []Volume

	if copySnapshots && !srcVol.IsSnapshot() {
		// Get the list of snapshots from the source.
		srcSnapshots, err = srcVol.Snapshots(op)
		if err != nil {
			return err
		}
	}

	// Run the generic copy.
	return genericVFSCopyVolume(d, d.setupInitialQuota, vol, srcVol, srcSnapshots, false, allowInconsistent, op)
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *dir) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	return genericVFSCreateVolumeFromMigration(d, d.setupInitialQuota, vol, conn, volTargetArgs, preFiller, op)
}

// RefreshVolume provides same-pool volume and specific snapshots syncing functionality.
func (d *dir) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, allowInconsistent bool, op *operations.Operation) error {
	return genericVFSCopyVolume(d, d.setupInitialQuota, vol, srcVol, srcSnapshots, true, allowInconsistent, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
func (d *dir) DeleteVolume(vol Volume, op *operations.Operation) error {
	snapshots, err := d.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volPath := vol.MountPath()

	// If the volume doesn't exist, then nothing more to do.
	if !util.PathExists(volPath) {
		return nil
	}

	// Get the volume ID for the volume, which is used to remove project quota.
	if vol.Type() != VolumeTypeBucket {
		volID, err := d.getVolID(vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Remove the project quota.
		err = d.deleteQuota(volPath, volID)
		if err != nil {
			return err
		}
	}

	// Remove the volume from the storage device.
	err = forceRemoveAll(volPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", volPath, err)
	}

	// Although the volume snapshot directory should already be removed, lets remove it here
	// to just in case the top-level directory is left.
	err = deleteParentSnapshotDirIfEmpty(d.name, vol.volType, vol.name)
	if err != nil {
		return err
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *dir) HasVolume(vol Volume) (bool, error) {
	return genericVFSHasVolume(vol)
}

// FillVolumeConfig populate volume with default config.
func (d *dir) FillVolumeConfig(vol Volume) error {
	initialSize := vol.config["size"]

	err := d.fillVolumeConfig(&vol)
	if err != nil {
		return err
	}

	// Buckets do not support default volume size.
	// If size is specified manually, do not remove, so it triggers validation failure and an error to user.
	if vol.volType == VolumeTypeBucket && initialSize == "" {
		delete(vol.config, "size")
	}

	return nil
}

// ValidateVolume validates the supplied volume config. Optionally removes invalid keys from the volume's config.
func (d *dir) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	err := d.validateVolume(vol, nil, removeUnknownKeys)
	if err != nil {
		return err
	}

	if vol.config["size"] != "" && vol.volType == VolumeTypeBucket {
		return fmt.Errorf("Size cannot be specified for buckets")
	}

	return nil
}

// UpdateVolume applies config changes to the volume.
func (d *dir) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *dir) GetVolumeUsage(vol Volume) (int64, error) {
	// Snapshot usage not supported for Dir.
	if vol.IsSnapshot() {
		return -1, ErrNotSupported
	}

	volPath := vol.MountPath()
	ok, err := quota.Supported(volPath)
	if err != nil || !ok {
		return -1, ErrNotSupported
	}

	// Get the volume ID for the volume to access quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return -1, err
	}

	projectID := d.quotaProjectID(volID)

	// Get project quota used.
	size, err := quota.GetProjectUsage(volPath, projectID)
	if err != nil {
		return -1, err
	}

	return size, nil
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size for block volumes, and for filesystem volumes removes quota.
func (d *dir) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// For VM block files, resize the file if needed.
	if vol.contentType == ContentTypeBlock {
		// Do nothing if size isn't specified.
		if sizeBytes <= 0 {
			return nil
		}

		rootBlockPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		resized, err := ensureVolumeBlockFile(vol, rootBlockPath, sizeBytes, allowUnsafeResize)
		if err != nil {
			return err
		}

		// Move the GPT alt header to end of disk if needed and resize has taken place (not needed in
		// unsafe resize mode as it is expected the caller will do all necessary post resize actions
		// themselves).
		if vol.IsVMBlock() && resized && !allowUnsafeResize {
			err = d.moveGPTAltHeader(rootBlockPath)
			if err != nil {
				return err
			}
		}

		return nil
	} else if vol.Type() != VolumeTypeBucket {
		// For non-VM block volumes, set filesystem quota.
		volID, err := d.getVolID(vol.volType, vol.name)
		if err != nil {
			return err
		}

		// Custom handling for filesystem volume associated with a VM.
		volPath := vol.MountPath()
		if sizeBytes > 0 && vol.volType == VolumeTypeVM && util.PathExists(filepath.Join(volPath, genericVolumeDiskFile)) {
			// Get the size of the VM image.
			blockSize, err := BlockDiskSizeBytes(filepath.Join(volPath, genericVolumeDiskFile))
			if err != nil {
				return err
			}

			// Add that to the requested filesystem size (to ignore it from the quota).
			sizeBytes += blockSize
			d.logger.Debug("Accounting for VM image file size", logger.Ctx{"sizeBytes": sizeBytes})
		}

		return d.setQuota(vol.MountPath(), volID, sizeBytes)
	}

	return nil
}

// GetVolumeDiskPath returns the location of a disk volume.
func (d *dir) GetVolumeDiskPath(vol Volume) (string, error) {
	return genericVFSGetVolumeDiskPath(vol)
}

// ListVolumes returns a list of volumes in storage pool.
func (d *dir) ListVolumes() ([]Volume, error) {
	return genericVFSListVolumes(d)
}

// MountVolume simulates mounting a volume.
func (d *dir) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	// Don't attempt to modify the permission of an existing custom volume root.
	// A user inside the instance may have modified this and we don't want to reset it on restart.
	if !util.PathExists(vol.MountPath()) || vol.volType != VolumeTypeCustom {
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	return nil
}

// UnmountVolume simulates unmounting a volume.
// As driver doesn't have volumes to unmount it returns false indicating the volume was already unmounted.
func (d *dir) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
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

// RenameVolume renames a volume and its snapshots.
func (d *dir) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	return genericVFSRenameVolume(d, vol, newVolName, op)
}

// MigrateVolume sends a volume for migration.
func (d *dir) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume copies a volume (and optionally its snapshots) to a specified target path.
// This driver does not support optimized backups.
func (d *dir) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *dir) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Create snapshot directory.
	err := snapVol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	snapPath := snapVol.MountPath()
	revert.Add(func() { _ = os.RemoveAll(snapPath) })

	if snapVol.contentType != ContentTypeBlock || snapVol.volType != VolumeTypeCustom {
		var rsyncArgs []string

		if snapVol.IsVMBlock() {
			rsyncArgs = append(rsyncArgs, "--exclude", genericVolumeDiskFile)
		}

		bwlimit := d.config["rsync.bwlimit"]
		srcPath := GetVolumeMountPath(d.name, snapVol.volType, parentName)
		d.Logger().Debug("Copying fileystem volume", logger.Ctx{"sourcePath": srcPath, "targetPath": snapPath, "bwlimit": bwlimit, "rsyncArgs": rsyncArgs})

		// Copy filesystem volume into snapshot directory.
		_, err = rsync.LocalCopy(srcPath, snapPath, bwlimit, true, rsyncArgs...)
		if err != nil {
			return err
		}
	}

	if snapVol.IsVMBlock() || (snapVol.contentType == ContentTypeBlock && snapVol.volType == VolumeTypeCustom) {
		parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, d.config)
		srcDevPath, err := d.GetVolumeDiskPath(parentVol)
		if err != nil {
			return err
		}

		targetDevPath, err := d.GetVolumeDiskPath(snapVol)
		if err != nil {
			return err
		}

		d.Logger().Debug("Copying block volume", logger.Ctx{"srcDevPath": srcDevPath, "targetPath": targetDevPath})

		err = ensureSparseFile(targetDevPath, 0)
		if err != nil {
			return err
		}

		err = copyDevice(srcDevPath, targetDevPath)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *dir) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	snapPath := snapVol.MountPath()

	// Remove the snapshot from the storage device.
	err := forceRemoveAll(snapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove '%s': %w", snapPath, err)
	}

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	return nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *dir) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	snapPath := snapVol.MountPath()

	// Don't attempt to modify the permission of an existing custom volume root.
	// A user inside the instance may have modified this and we don't want to reset it on restart.
	if !util.PathExists(snapPath) || snapVol.volType != VolumeTypeCustom {
		err := snapVol.EnsureMountPath()
		if err != nil {
			return err
		}
	}

	_, err = mountReadOnly(snapPath, snapPath)
	if err != nil {
		return err
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	return nil
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
func (d *dir) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	mountPath := snapVol.MountPath()

	refCount := snapVol.MountRefCountDecrement()

	if linux.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
			return false, ErrInUse
		}

		snapPath := snapVol.MountPath()
		return forceUnmount(snapPath)
	}

	return false, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *dir) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	return genericVFSVolumeSnapshots(d, vol, op)
}

// RestoreVolume restores a volume from a snapshot.
func (d *dir) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	snapVol, err := vol.NewSnapshot(snapshotName)
	if err != nil {
		return err
	}

	srcPath := snapVol.MountPath()
	if !util.PathExists(srcPath) {
		return fmt.Errorf("Snapshot not found")
	}

	volPath := vol.MountPath()

	// Restore filesystem volume.
	if vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom {
		var rsyncArgs []string

		if vol.IsVMBlock() {
			rsyncArgs = append(rsyncArgs, "--exclude", genericVolumeDiskFile)
		}

		bwlimit := d.config["rsync.bwlimit"]
		_, err := rsync.LocalCopy(srcPath, volPath, bwlimit, true, rsyncArgs...)
		if err != nil {
			return fmt.Errorf("Failed to rsync volume: %w", err)
		}
	}

	// Restore block volume.
	if vol.IsVMBlock() || (vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom) {
		srcDevPath, err := d.GetVolumeDiskPath(snapVol)
		if err != nil {
			return err
		}

		targetDevPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		d.Logger().Debug("Restoring block volume", logger.Ctx{"srcDevPath": srcDevPath, "targetPath": targetDevPath})

		err = ensureSparseFile(targetDevPath, 0)
		if err != nil {
			return err
		}

		err = copyDevice(srcDevPath, targetDevPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *dir) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	return genericVFSRenameVolumeSnapshot(d, snapVol, newSnapshotName, op)
}
