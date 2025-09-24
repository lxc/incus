package drivers

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	"github.com/lxc/incus/v6/internal/server/backup"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *truenas) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	// Revert handling
	reverter := revert.New()
	defer reverter.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath(true)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// Look for previously deleted images. (don't look for underlying, or we'll look after we've looked)
	if vol.volType == VolumeTypeImage {
		dataset := d.dataset(vol, true)
		exists, err := d.datasetExists(dataset)
		if err != nil {
			return err
		}

		if exists {
			canRestore := true

			// check if the cached image volume is larger than the current pool volume.size setting (if so we won't be
			// able to resize the snapshot to that the smaller size later).
			volSize, err := d.getDatasetProperty(dataset, "volsize")
			if err != nil {
				return err
			}

			volSizeBytes, err := strconv.ParseInt(volSize, 10, 64)
			if err != nil {
				return err
			}

			poolVolSize := DefaultBlockSize
			if vol.poolConfig["volume.size"] != "" {
				poolVolSize = vol.poolConfig["volume.size"]
			}

			poolVolSizeBytes, err := units.ParseByteSizeString(poolVolSize)
			if err != nil {
				return err
			}

			// Round to block boundary.
			poolVolSizeBytes, err = d.roundVolumeBlockSizeBytes(vol, poolVolSizeBytes)
			if err != nil {
				return err
			}

			// If the cached volume size is different than the pool volume size, then we can't use the
			// deleted cached image volume and instead we will rename it to a random UUID so it can't
			// be restored in the future and a new cached image volume will be created instead.
			if volSizeBytes != poolVolSizeBytes {
				d.logger.Debug("Renaming deleted cached image volume so that regeneration is used", logger.Ctx{"fingerprint": vol.Name()})
				randomVol := NewVolume(d, d.name, vol.volType, vol.contentType, d.randomVolumeName(vol), vol.config, vol.poolConfig)

				_, err := d.runTool("dataset", "rename", dataset, d.dataset(randomVol, true))
				if err != nil {
					return err
				}

				if vol.IsVMBlock() {
					fsVol := vol.NewVMBlockFilesystemVolume()
					randomFsVol := randomVol.NewVMBlockFilesystemVolume()

					_, err := d.runTool("dataset", "rename", d.dataset(fsVol, true), d.dataset(randomFsVol, true))
					if err != nil {
						return err
					}
				}

				// We have renamed the deleted cached image volume, so we don't want to try and
				// restore it.
				canRestore = false
			}

			// Restore the image.
			if canRestore {
				d.logger.Debug("Restoring previously deleted cached image volume", logger.Ctx{"fingerprint": vol.Name()})
				_, err := d.runTool("dataset", "rename", dataset, d.dataset(vol, false))
				if err != nil {
					return err
				}

				// After this point we have a restored image, so setup reverter.
				reverter.Add(func() { _ = d.DeleteVolume(vol, op) })

				if vol.IsVMBlock() {
					fsVol := vol.NewVMBlockFilesystemVolume()
					_, err := d.runTool("dataset", "rename", d.dataset(fsVol, true), d.dataset(fsVol, false))
					if err != nil {
						return err
					}

					// no need for reverter.add here as we have succeeded
				}

				reverter.Success()
				return nil
			}
		}
	}

	var opts []string

	// Add custom property incus:content_type which allows distinguishing between regular volumes, block_mode enabled volumes, and ISO volumes.
	if vol.volType == VolumeTypeCustom {
		opts = append(opts, fmt.Sprintf("user-props=incus:content_type=%s", vol.contentType))
	}

	blockSize := vol.ExpandedConfig("truenas.blocksize")
	if blockSize != "" {
		// Convert to bytes.
		sizeBytes, err := units.ParseByteSizeString(blockSize)
		if err != nil {
			return err
		}

		// volblocksize maximum value is 128KiB so if the value of truenas.blocksize is bigger set it to 128KiB.
		if sizeBytes > zfsMaxVolBlocksize {
			sizeBytes = zfsMaxVolBlocksize
		}

		opts = append(opts, fmt.Sprintf("volblocksize=%d", sizeBytes))
	}

	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return err
	}

	sizeBytes, err = d.roundVolumeBlockSizeBytes(vol, sizeBytes)
	if err != nil {
		return err
	}

	dataset := d.dataset(vol, false)

	// Create the volume dataset.
	err = d.createVolume(dataset, sizeBytes, opts...)
	if err != nil {
		return err
	}

	// After this point we'll have a volume, so setup reverter.
	reverter.Add(func() { _ = d.DeleteVolume(vol, op) })

	err = d.createIscsiShare(dataset, false)
	if err != nil {
		return err
	}

	if vol.contentType == ContentTypeFS {
		// activateIscsiDataset does not check if the dataset has been activated.
		// devPath, err := d.activateIscsiDataset(dataset)
		_, devPath, err := d.locateOrActivateIscsiDataset(dataset)
		if err != nil {
			return err
		}

		fsVolFilesystem := vol.ConfigBlockFilesystem()

		_, err = makeFSType(devPath, fsVolFilesystem, nil)

		// de-activate even if there is an err
		err2 := d.deactivateIscsiDataset(dataset)

		if err != nil {
			return err
		}

		if err2 != nil {
			return err2
		}
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = d.DeleteVolume(fsVol, op) })
	}

	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// Run the volume filler function if supplied.
		if filler != nil && filler.Fill != nil {
			var err error
			var devPath string

			if IsContentBlock(vol.contentType) {
				// Get the device path.
				devPath, err = d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}
			}

			allowUnsafeResize := false
			if vol.volType == VolumeTypeImage {
				// Allow filler to resize initial image volume as needed.
				// Some storage drivers don't normally allow image volumes to be resized due to
				// them having read-only snapshots that cannot be resized. However when creating
				// the initial image volume and filling it before the snapshot is taken resizing
				// can be allowed and is required in order to support unpacking images larger than
				// the default volume size. The filler function is still expected to obey any
				// volume size restrictions configured on the pool.
				// Unsafe resize is also needed to disable filesystem resize safety checks.
				// This is safe because if for some reason an error occurs the volume will be
				// discarded rather than leaving a corrupt filesystem.
				allowUnsafeResize = true
			}

			// Run the filler.
			err = d.runFiller(vol, devPath, filler, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Move the GPT alt header to end of disk if needed.
			if vol.IsVMBlock() {
				err = d.moveGPTAltHeader(devPath)
				if err != nil {
					return err
				}
			}
		}

		if vol.contentType == ContentTypeFS {
			// Run EnsureMountPath again after mounting and filling to ensure the mount directory has
			// the correct permissions set.
			err := vol.EnsureMountPath(true)
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	// Setup snapshot and unset mountpoint on image.
	if vol.volType == VolumeTypeImage {
		// ideally, we don't want to snap the underlying when we create the img, but rather after we've unpacked.
		// note: we may need to sync the underlying filesystem, it depends if its still mounted, I think it shouldn't be.

		dataset := d.dataset(vol, false)
		snapName := fmt.Sprintf("%s@readonly", dataset)

		// Create snapshot of the main dataset.
		err := d.createSnapshot(snapName, false)
		if err != nil {
			return err
		}

		if vol.contentType == ContentTypeBlock {
			// Re-create the FS config volume's readonly snapshot now that the filler function has run
			// and unpacked into both config and block volumes.
			fsVol := vol.NewVMBlockFilesystemVolume()
			snapName = fmt.Sprintf("%s@readonly", d.dataset(fsVol, false))

			err := d.createSnapshot(snapName, true) // delete, then snap.
			if err != nil {
				return err
			}
		}
	}

	// All done.
	reverter.Success()

	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *truenas) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	// TODO: optimized version

	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// same as CreateVolumeFromCopy, but will refresh if refresh is true.
func (d *truenas) createOrRefeshVolumeFromCopy(vol Volume, srcVol Volume, refresh bool, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	var err error

	// Revert handling
	reverter := revert.New()
	defer reverter.Fail()

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err = vol.EnsureMountPath(false)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// For VMs, also copy the filesystem dataset.
	if vol.IsVMBlock() {
		// For VMs, also copy the filesystem volume.
		srcFSVol := srcVol.NewVMBlockFilesystemVolume()
		fsVol := vol.NewVMBlockFilesystemVolume()

		err = d.createOrRefeshVolumeFromCopy(fsVol, srcFSVol, refresh, copySnapshots, false, op)
		if err != nil {
			return err
		}

		// Delete on revert.
		if !refresh {
			reverter.Add(func() { _ = d.DeleteVolume(fsVol, op) })
		}
	}

	// Retrieve snapshots on the source.
	snapshots := []string{}
	if !srcVol.IsSnapshot() && copySnapshots {
		snapshots, err = d.VolumeSnapshots(srcVol, op)
		if err != nil {
			return err
		}
	}

	// When not allowing inconsistent copies and the volume has a mounted filesystem, we must ensure it is
	// consistent by syncing and freezing the filesystem to ensure unwritten pages are flushed and that no
	// further modifications occur while taking the source snapshot.
	var unfreezeFS func() error
	sourcePath := srcVol.MountPath()
	if !allowInconsistent && srcVol.contentType == ContentTypeFS && linux.IsMountPoint(sourcePath) {
		unfreezeFS, err = d.filesystemFreeze(sourcePath)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = unfreezeFS() })
	}

	srcDataset := d.dataset(srcVol, false)

	var srcSnapshot string
	if srcVol.volType == VolumeTypeImage {
		srcSnapshot = fmt.Sprintf("%s@readonly", srcDataset)
	} else if srcVol.IsSnapshot() {
		srcSnapshot = srcDataset
	} else {
		// Create a new snapshot for copy.
		srcSnapshot = fmt.Sprintf("%s@copy-%s", srcDataset, uuid.New().String())

		err := d.createSnapshot(srcSnapshot, false)
		if err != nil {
			return err
		}

		// If truenas.clone_copy is disabled delete the snapshot at the end.
		if util.IsFalse(d.config["truenas.clone_copy"]) || len(snapshots) > 0 {
			// Delete the snapshot at the end.
			defer func() {
				// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
				err = d.deleteSnapshot(srcSnapshot, true, "defer")
				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			}()
		} else {
			// Delete the snapshot on revert.
			reverter.Add(func() {
				// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
				err = d.deleteSnapshot(srcSnapshot, true, "defer")
				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			})
		}
	}

	// Now that source snapshot has been taken we can safely unfreeze the source filesystem.
	if unfreezeFS != nil {
		_ = unfreezeFS()
	}

	// Delete the volume created on failure.
	if !refresh {
		reverter.Add(func() { _ = d.DeleteVolume(vol, op) })
	}

	destDataset := d.dataset(vol, false)

	// If truenas.clone_copy is disabled or source volume has snapshots, then use full copy mode.
	if util.IsFalse(d.config["truenas.clone_copy"]) || len(snapshots) > 0 {
		// Run the replication, snaps + copy- snap. TODO: verify necessary props are replicated.
		args := []string{"replication", "start", "--recursive", "--readonly-policy=ignore"}

		if refresh {
			/*
				refresh is essentially an optimized form of replace.

				refresh implies that we may have a dest already, and since the source may be unrelated,
				we may need to replicate from scratch. The retention policy ensures obsoleted snaps are
				removed from the dest.
			*/
			args = append(args, "--retention-policy=source", "--allow-from-scratch=true")
		}

		/*
			instead of using full replication, and then removing snapshots, we instead take advantage of the replication task's
			ability to filter snapshots as they are sent.
		*/
		snapName := strings.SplitN(srcSnapshot, "@", 2)[1]
		snapRegex := fmt.Sprintf("(snapshot-.*|%s)", snapName)

		args = append(args, "--name-regex", snapRegex, srcDataset, destDataset)

		_, err := d.runTool(args...)
		if err != nil {
			return fmt.Errorf("Failed to replicate dataset: %w", err)
		}

		// Delete the copy- snapshot on the dest.
		err = d.deleteSnapshot(fmt.Sprintf("%s@%s", destDataset, snapName), true)
		if err != nil {
			return err
		}
	} else {
		// Perform volume clone.
		err = d.cloneSnapshot(srcSnapshot, destDataset)
		if err != nil {
			return err
		}

		// Note: user props aren't cloned, so we re-add the content_type if necessary
		if vol.volType == VolumeTypeCustom {
			// Add custom property incus:content_type which allows distinguishing between regular volumes, block_mode enabled volumes, and ISO volumes.
			props := fmt.Sprintf("user-props=incus:content_type=%s", vol.contentType) // TODO: this needs to be better.
			err = d.setDatasetProperties(destDataset, props)
			if err != nil {
				return err
			}
		}
	}

	// and share the clone/copy.
	err = d.createIscsiShare(destDataset, false)
	if err != nil {
		return err
	}

	// Apply the properties.
	if vol.contentType == ContentTypeFS {
		if renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
			// regen must be done with vol unmounted.

			_, volPath, err := d.activateVolume(vol)
			if err != nil {
				return err
			}

			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": vol.ConfigBlockFilesystem()})
			err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volPath)
			if err != nil {
				return err
			}
		}

		// Mount the volume and ensure the permissions are set correctly inside the mounted volume.
		err := vol.MountTask(func(_ string, _ *operations.Operation) error {
			return vol.EnsureMountPath(false)
		}, op)
		if err != nil {
			return err
		}
	}

	// Pass allowUnsafeResize as true when resizing block backed filesystem volumes because we want to allow
	// the filesystem to be shrunk as small as possible without needing the safety checks that would prevent
	// leaving the filesystem in an inconsistent state if the resize couldn't be completed. This is because if
	// the resize fails we will delete the volume anyway so don't have to worry about it being inconsistent.
	var allowUnsafeResize bool
	if vol.contentType == ContentTypeFS {
		allowUnsafeResize = true
	}

	// Resize volume to the size specified. Only uses volume "size" property and does not use pool/defaults
	// to give the caller more control over the size being used.
	err = d.SetVolumeQuota(vol, vol.config["size"], allowUnsafeResize, op)
	if err != nil {
		return err
	}

	// All done.
	reverter.Success()
	return nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *truenas) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	return d.createOrRefeshVolumeFromCopy(vol, srcVol, false, copySnapshots, allowInconsistent, op) // not refreshing.
}

// CreateVolumeFromMigration creates a volume being sent via a migration. TODO: need to ensure that incus:content_type is copied.
func (d *truenas) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs localMigration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if volTargetArgs.ClusterMoveSourceName != "" && volTargetArgs.StoragePool == "" {
		d.logger.Debug("Detected migration between cluster members on the same storage pool")
		err := vol.EnsureMountPath(false)
		if err != nil {
			return err
		}

		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			err := d.CreateVolumeFromMigration(fsVol, conn, volTargetArgs, preFiller, op)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Handle simple rsync and block_and_rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volTargetArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		return genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	}

	// TODO: optimized migration

	return ErrNotSupported
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *truenas) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, allowInconsistent bool, op *operations.Operation) error {
	var err error
	var targetSnapshots []Volume
	var srcSnapshotsAll []Volume

	if !srcVol.IsSnapshot() {
		// Get target snapshots
		targetSnapshots, err = vol.Snapshots(op)
		if err != nil {
			return fmt.Errorf("Failed to get target snapshots: %w", err)
		}

		srcSnapshotsAll, err = srcVol.Snapshots(op)
		if err != nil {
			return fmt.Errorf("Failed to get source snapshots: %w", err)
		}
	}

	// If there are no source or target snapshots, perform a simple replacement copy
	if len(srcSnapshotsAll) == 0 || len(targetSnapshots) == 0 {
		// this ensures that recursive deletions are performed.
		err = d.DeleteVolume(vol, op)
		if err != nil {
			return err
		}

		return d.CreateVolumeFromCopy(vol, srcVol, len(srcSnapshotsAll) == 0, false, op)
	}

	// repl task can "refresh"
	return d.createOrRefeshVolumeFromCopy(vol, srcVol, true, true, false, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then
// this function will return an error.
// For image volumes, both filesystem and block volumes will be removed.
func (d *truenas) DeleteVolume(vol Volume, op *operations.Operation) error {
	if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS {
		// deletes all block.filesystem permutations
		return d.deleteImageFsVolume(vol, op)
	}

	return d.deleteVolume(vol, nil, op)
}

// deleteImageFsVolume efficiently deletes all filesystem variations of an ImageFS (use for vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS ).
func (d *truenas) deleteImageFsVolume(vol Volume, op *operations.Operation) error {
	if vol.volType != VolumeTypeImage || vol.contentType != ContentTypeFS {
		return fmt.Errorf("deleteImageFsVolume called on invalid volume: %v", vol)
	}

	/*
		the basic idea is to avoid the iterative existence checks for each filesystem, since we expect all but one not to exist
	*/

	// We need to clone vol the otherwise changing `block.filesystem` in tmpVol will also change it in vol.
	tmpVol := vol.Clone()

	// form a list of FSs without the actual volume's FS.
	fsList := []string{}
	volFs := vol.ConfigBlockFilesystem()
	for _, filesystem := range blockBackedAllowedFilesystems {
		if filesystem == volFs {
			continue
		}

		fsList = append(fsList, filesystem)
	}

	// generate a list of all the datasets to be existence checked
	datasets := []string{d.dataset(vol, false)}
	for _, filesystem := range fsList {
		tmpVol.config["block.filesystem"] = filesystem
		datasets = append(datasets, d.dataset(tmpVol, false))
	}

	// returns a map of all the datasets existence, including those that don't exist.
	existsMap, err := d.objectsExist(datasets, "dataset")
	if err != nil {
		return fmt.Errorf("Unable to verify existence of FS Images, Error: %w", err)
	}

	// delete all the other file systems
	for _, filesystem := range fsList {
		tmpVol.config["block.filesystem"] = filesystem

		dataset := d.dataset(tmpVol, false)
		exists, ok := existsMap[dataset]
		if ok && exists {
			_ = d.deleteVolume(tmpVol, &exists, op)
		}
	}

	// and finally, delete the actual volume, with whatever its FS is that we specifically skipped earlier.
	dataset := d.dataset(vol, false)
	exists, ok := existsMap[dataset]
	if !ok {
		return fmt.Errorf("Unable to retrieve existence of FS Image: %s", dataset)
	}

	// cleans up mount points etc
	err = d.deleteVolume(vol, &exists, op)
	if err != nil {
		return err
	}

	return nil
}

// deleteVolume deletes the volume if it exists, and cleans up, pass optionalExistance if you know.
func (d *truenas) deleteVolume(vol Volume, optionalExistance *bool, op *operations.Operation) error {
	// Check that we have a dataset to delete.
	dataset := d.dataset(vol, false)

	var exists bool

	// allows performing bulk existence checks.
	if optionalExistance != nil {
		exists = *optionalExistance
	} else {
		e, err := d.datasetExists(dataset)
		if err != nil {
			return err
		}

		exists = e // declared and not used: exists
	}

	if exists {
		// Deleted volumes do not need shares
		_ = d.deleteIscsiShare(dataset) // will implicitly deactivate, if activated.

		// Handle clones.
		clones, err := d.getClones(dataset)
		if err != nil {
			return err
		}

		if len(clones) > 0 {
			// Move to the deleted path.
			err := d.renameDataset(dataset, d.dataset(vol, true), false)
			if err != nil {
				return err
			}
		} else {
			err := d.deleteDatasetRecursive(dataset)
			if err != nil {
				return err
			}
		}
	}

	if vol.contentType == ContentTypeFS {
		// Delete the mountpoint if present.
		err := os.Remove(vol.MountPath())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", vol.MountPath(), err)
		}

		// Delete the snapshot storage.
		err = os.RemoveAll(GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", GetVolumeSnapshotDir(d.name, vol.volType, vol.name), err)
		}
	}

	// For VMs, also delete the filesystem dataset.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolume(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *truenas) HasVolume(vol Volume) (bool, error) {
	// Check if the dataset exists.
	dataset := d.dataset(vol, false)
	return d.datasetExists(dataset)
}

// ValidateTrueNasVolBlocksize validates blocksize property value on the pool, matches volblocksize.
func ValidateTrueNasVolBlocksize(value string) error {
	/*
		For volumes, specifies the block size of the volume.  The blocksize cannot be changed once the volume has been written,
		so it should be set at volume creation time.  The default blocksize for volumes is 16 KiB.  Any power of 2 from 512 bytes to 128 KiB is valid.
	*/

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	if sizeBytes < zfsMinBlocksize || sizeBytes > zfsMaxVolBlocksize || (sizeBytes&(sizeBytes-1)) != 0 {
		return errors.New("Value should be between 512B and 128KiB, and be power of 2")
	}

	return nil
}

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *truenas) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		"block.filesystem":         validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		"block.mount_options":      validate.IsAny,
		"truenas.blocksize":        validate.Optional(ValidateTrueNasVolBlocksize), // used for volblocksize only. NOTE: zfs.blocksize is hard-coded in backend.shouldUseOptimizedImage...
		"truenas.remove_snapshots": validate.Optional(validate.IsBool),
		"truenas.use_refquota":     validate.Optional(validate.IsBool),
	}
}

// ValidateVolume validates the supplied volume config.
func (d *truenas) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	commonRules := d.commonVolumeRules()

	// Disallow block.* settings for regular custom block volumes. These settings only make sense
	// when using custom filesystem volumes. Incus will create the filesystem
	// for these volumes, and use the mount options. When attaching a regular block volume to a VM,
	// these are not mounted by Incus and therefore don't need these config keys.
	if vol.IsVMBlock() || vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		delete(commonRules, "block.filesystem")
		delete(commonRules, "block.mount_options")
	}

	return d.validateVolume(vol, commonRules, removeUnknownKeys)
}

// UpdateVolume applies config changes to the volume.
func (d *truenas) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	// Mangle the current volume to its old values.
	old := make(map[string]string)
	for k, v := range changedConfig {
		if k == "size" || k == "truenas.use_refquota" {
			old[k] = vol.config[k]
			vol.config[k] = v
		}
	}

	defer func() {
		for k, v := range old {
			vol.config[k] = v
		}
	}()

	// If any of the relevant keys changed, re-apply the quota.
	if len(old) != 0 {
		err := d.SetVolumeQuota(vol, vol.ExpandedConfig("size"), false, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// CacheVolumeSnapshots fetches snapshot usage properties for all snapshots on the volume.
func (d *truenas) CacheVolumeSnapshots(vol Volume) error {
	// NOTE: this actually gets info for all datasets and snapshots.

	// Lock the cache.
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()

	// Check if we've already cached the data.
	if d.cache != nil {
		return nil
	}

	// Get the usage data.
	out, err := d.runTool("list", "--no-headers", "--parsable", "-o", "name,used,referenced", "-r", "-t", "snap,fs,vol", d.dataset(vol, false))
	if err != nil {
		d.logger.Warn("Coulnd't list volume snapshots", logger.Ctx{"err": err})

		// The cache is an optional performance improvement, don't block on failure.
		return nil
	}

	// Parse and update the cache.
	d.cache = map[string]map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}

		usedInt, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		referencedInt, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}

		d.cache[fields[0]] = map[string]int64{
			"used":       usedInt,
			"referenced": referencedInt,
		}
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume.
func (d *truenas) GetVolumeUsage(vol Volume) (int64, error) {
	// Determine what key to use.
	key := "used"

	// If volume isn't snapshot then we can take into account the truenas.use_refquota setting.
	// Snapshots should also use the "used" ZFS property because the snapshot usage size represents the CoW
	// usage not the size of the snapshot volume.
	if !vol.IsSnapshot() {
		if util.IsTrue(vol.ExpandedConfig("truenas.use_refquota")) {
			key = "referenced"
		}

		// Shortcut for mounted refquota filesystems.
		if key == "referenced" && vol.contentType == ContentTypeFS && linux.IsMountPoint(vol.MountPath()) {
			var stat unix.Statfs_t
			err := unix.Statfs(vol.MountPath(), &stat)
			if err != nil {
				return -1, err
			}

			return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
		}
	}

	// Try to use the cached data.
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()

	dataset := d.dataset(vol, false)
	if d.cache != nil {
		cache, ok := d.cache[dataset]
		if ok {
			value, ok := cache[key]
			if ok {
				return value, nil
			}
		}
	}

	// Get the current value.
	value, err := d.getDatasetProperty(dataset, key)
	if err != nil {
		return -1, err
	}

	// Convert to int.
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, err
	}

	return valueInt, nil
}

// SetVolumeQuota sets the quota/reservation on the volume.
// Does nothing if supplied with an empty/zero size for block volumes.
func (d *truenas) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	inUse := vol.MountInUse()
	dataset := d.dataset(vol, false)

	// always zvols with blockbacking.

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	sizeBytes, err = d.roundVolumeBlockSizeBytes(vol, sizeBytes)
	if err != nil {
		return err
	}

	oldSizeBytesStr, err := d.getDatasetProperty(dataset, "volsize")
	if err != nil {
		return err
	}

	oldVolSizeBytesInt, err := strconv.ParseInt(oldSizeBytesStr, 10, 64)
	if err != nil {
		return err
	}

	oldVolSizeBytes := int64(oldVolSizeBytesInt)

	if oldVolSizeBytes == sizeBytes {
		return nil
	}

	if vol.contentType == ContentTypeFS {
		// Activate volume if needed.
		activated, volDevPath, err := d.activateVolume(vol)
		if err != nil {
			return err
		}

		if activated {
			defer func() { _, _ = d.deactivateVolume(vol) }()
		}

		if vol.volType == VolumeTypeImage {
			return fmt.Errorf("Image volumes cannot be resized: %w", ErrCannotBeShrunk)
		}

		fsType := vol.ConfigBlockFilesystem()

		l := d.logger.AddContext(logger.Ctx{"dev": volDevPath, "size": fmt.Sprintf("%db", sizeBytes)})

		if sizeBytes < oldVolSizeBytes {
			if !filesystemTypeCanBeShrunk(fsType) {
				return fmt.Errorf("Filesystem %q cannot be shrunk: %w", fsType, ErrCannotBeShrunk)
			}

			if inUse {
				return ErrInUse // We don't allow online shrinking of filesystem block volumes.
			}

			// Shrink filesystem first.
			// Pass allowUnsafeResize to allow disabling of filesystem resize safety checks.
			err = shrinkFileSystem(fsType, volDevPath, vol, sizeBytes, allowUnsafeResize)
			if err != nil {
				return err
			}

			l.Debug("TrueNAS volume filesystem shrunk")

			// Shrink the block device.
			err = d.setVolsize(dataset, sizeBytes, true) // allow shrink, shrink errors will be ignored.
			if err != nil {
				return err
			}
		} else if sizeBytes > oldVolSizeBytes {
			// Grow block device first, ignoring any shrink errors, which could happen because we've
			// already ignored a shrink error when shrinking.
			err = d.setVolsize(dataset, sizeBytes, false)
			if err != nil {
				return err
			}

			// Grow the filesystem to fill block device.
			err = growFileSystem(fsType, volDevPath, vol)
			if err != nil {
				return err
			}

			l.Debug("TrueNAS volume filesystem grown")
		}
	} else {
		// Block image volumes cannot be resized because they have a readonly snapshot that doesn't get
		// updated when the volume's size is changed, and this is what instances are created from.
		// During initial volume fill allowUnsafeResize is enabled because snapshot hasn't been taken yet.
		if !allowUnsafeResize && vol.volType == VolumeTypeImage {
			return ErrNotSupported
		}

		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize {
			if sizeBytes < oldVolSizeBytes {
				return fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
			}
		}

		// Adjust zvol size
		err = d.setVolsize(dataset, sizeBytes, true)
		if err != nil {
			return err
		}
	}

	// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as
	// it is expected the caller will do all necessary post resize actions themselves).
	if vol.IsVMBlock() && !allowUnsafeResize {
		err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
			devPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			return d.moveGPTAltHeader(devPath)
		}, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// getTempSnapshotVolName returns a derived volume name for the server specific clone of the specified snapshot volume.
func (d *truenas) getTempSnapshotVolName(vol Volume) string {
	parent, snapshotOnlyName, _ := api.GetParentAndSnapshotName(vol.Name())
	parentVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, parent, vol.config, vol.poolConfig)
	parentDataset := d.dataset(parentVol, false)

	// serverName to allow other cluster members to mount the same snapshot at the same time.
	dataset := fmt.Sprintf("%s_%s_%s-%d%s", parentDataset, snapshotOnlyName, d.state.ServerName, os.Getpid(), tmpVolSuffix)

	return dataset
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *truenas) GetVolumeDiskPath(vol Volume) (string, error) {
	var dataset string

	if vol.IsSnapshot() {
		dataset = d.getTempSnapshotVolName(vol)
	} else {
		dataset = d.dataset(vol, false)
	}

	return d.locateIscsiDataset(dataset)
}

// ListVolumes returns a list of volumes in storage pool.
func (d *truenas) ListVolumes() ([]Volume, error) {
	vols := make(map[string]Volume)
	_ = vols

	/* from backend.ListUnknownVolumes
	// Get a list of volumes on the storage pool. We only expect to get 1 volume per logical Incus volume.
	// So for VMs we only expect to get the block volume for a VM and not its filesystem one too. This way we
	// can operate on the volume using the existing storage pool functions and let the pool then handle the
	// associated filesystem volume as needed.
	*/

	// Get just filesystem and volume datasets, not snapshots.
	// The ZFS driver uses two approaches to indicating block volumes; firstly for VM and image volumes it
	// creates both a filesystem dataset and an associated volume ending in zfsBlockVolSuffix.
	// However for custom block volumes it does not also end the volume name in zfsBlockVolSuffix (unlike the
	// LVM and Ceph drivers), so we must also retrieve the dataset type here and look for "volume" types
	// which also indicate this is a block volume.
	out, err := d.runTool("list", "--no-headers", "-o", "name,incus:content_type", "-r", "-t", "volume", d.config["truenas.dataset"])
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(out))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			return nil, fmt.Errorf("Unexpected volume line %q", line)
		}

		zfsVolName := parts[0]
		incusContentType := parts[1]

		var volType VolumeType
		var volName string
		var volFs string

		for _, volumeType := range d.Info().VolumeTypes {
			prefix := fmt.Sprintf("%s/%s/", d.config["truenas.dataset"], volumeType)
			if strings.HasPrefix(zfsVolName, prefix) {
				volType = volumeType
				volName = strings.TrimPrefix(zfsVolName, prefix)
			}
		}

		if volType == "" {
			d.logger.Debug("Ignoring unrecognised volume type", logger.Ctx{"name": zfsVolName})
			continue // Ignore unrecognised volume.
		}

		contentType := ContentTypeFS

		if volType == VolumeTypeVM && !strings.HasSuffix(volName, zfsBlockVolSuffix) {
			continue // Ignore VM filesystem volumes as we will just return the VM's block volume.
		}

		if volType == VolumeTypeCustom && strings.HasSuffix(volName, zfsISOVolSuffix) {
			contentType = ContentTypeISO
			volName = strings.TrimSuffix(volName, zfsISOVolSuffix)
		} else if volType == VolumeTypeVM || (volType == VolumeTypeImage && strings.HasSuffix(volName, zfsBlockVolSuffix)) {
			contentType = ContentTypeBlock
			volName = strings.TrimSuffix(volName, zfsBlockVolSuffix)
		}

		// FS images have the FS encoded after a _ separator
		if volType == VolumeTypeImage && strings.Contains(volName, "_") {
			volName, volFs, _ = strings.Cut(volName, "_")
		}

		// If a new volume has been found, or the volume will replace an existing image filesystem volume
		// then proceed to add the volume to the map. We allow image volumes to overwrite existing
		// filesystem volumes of the same name so that for VM images we only return the block content type
		// volume (so that only the single "logical" volume is returned).
		existingVol, foundExisting := vols[volName]
		if !foundExisting || (existingVol.Type() == VolumeTypeImage && existingVol.ContentType() == ContentTypeFS) {
			v := NewVolume(d, d.name, volType, contentType, volName, make(map[string]string), d.config)

			if volFs != "" {
				v.config["block.filesystem"] = volFs
			}

			// Get correct content type from incus:content_type property.
			if incusContentType != "-" {
				v.contentType = ContentType(incusContentType)
			}

			/*
				if its a filesystem, we need to probe it, unless we know the fs, but VMBlock's have an implicit filesystem Volume, and that Volume
				inherits the probe setting from the block volume.
			*/
			if (v.contentType == ContentTypeFS && volFs == "") || v.IsVMBlock() {
				v.SetMountFilesystemProbe(true)
			}

			vols[volName] = v
			continue
		}

		return nil, fmt.Errorf("Unexpected duplicate volume %q found", volName)
	}

	volList := make([]Volume, 0, len(vols))
	for _, v := range vols {
		volList = append(volList, v)
	}

	return volList, nil
}

// activateVolume activates a ZFS volume if not already active. Returns true if activated, false if not.
func (d *truenas) activateVolume(vol Volume) (bool, string, error) {
	if !IsContentBlock(vol.contentType) && !vol.IsBlockBacked() {
		return false, "", nil // Nothing to do for non-block or non-block backed volumes.
	}

	dataset := d.dataset(vol, false)

	// Check if already active.
	didActivate, devPath, err := d.locateOrActivateIscsiDataset(dataset)
	if err != nil {
		return false, "", err
	}

	if didActivate {
		d.logger.Debug("Activated TrueNAS volume", logger.Ctx{"volName": vol.Name(), "dev": dataset})
	}

	return didActivate, devPath, nil
}

// deactivateVolume deactivates a ZFS volume if activate. Returns true if deactivated, false if not.
func (d *truenas) deactivateVolume(vol Volume) (bool, error) {
	if vol.contentType != ContentTypeBlock && !vol.IsBlockBacked() {
		return false, nil // Nothing to do for non-block and non-block backed volumes.
	}

	dataset := d.dataset(vol, false)

	// Check if currently active.
	didDeactivate, err := d.deactivateIscsiDatasetIfActive(dataset)
	if err != nil {
		return false, fmt.Errorf("Failed deactivating TrueNAS volume: %w", err)
	}

	if didDeactivate {
		d.logger.Debug("Deactivated TrueNAS volume", logger.Ctx{"volName": vol.name, "dev": dataset})
	}

	return didDeactivate, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *truenas) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	reverter := revert.New()
	defer reverter.Fail()

	// Activate TrueNAS volume if needed.
	activated, volDevPath, err := d.activateVolume(vol)
	if err != nil {
		return err
	}

	if activated {
		reverter.Add(func() { _, _ = d.deactivateVolume(vol) })
	}

	switch vol.contentType {
	case ContentTypeFS:
		mountPath := vol.MountPath()
		if !linux.IsMountPoint(mountPath) {
			err := vol.EnsureMountPath(false)
			if err != nil {
				return err
			}

			fsType := vol.ConfigBlockFilesystem()

			if vol.mountFilesystemProbe {
				fsType, err = fsProbe(volDevPath)
				if err != nil {
					return fmt.Errorf("Failed probing filesystem: %w", err)
				}
			}

			mountFlags, mountOptions := linux.ResolveMountOptions(strings.Split(vol.ConfigBlockMountOptions(), ","))
			err = TryMount(volDevPath, mountPath, fsType, mountFlags, mountOptions)
			if err != nil {
				return err
			}

			d.logger.Debug("Mounted TrueNAS volume", logger.Ctx{"volName": vol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}

	case ContentTypeBlock:
		// For VMs, mount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			err = d.MountVolume(fsVol, op)
			if err != nil {
				return err
			}
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	reverter.Success()
	return nil
}

// UnmountVolume simulates unmounting a volume.
// keepBlockDev indicates if backing block device should be not be unmapped if volume is unmounted.
func (d *truenas) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := vol.MountPath()

	refCount := vol.MountRefCountDecrement()

	// Attempt to unmount the volume.
	if vol.contentType == ContentTypeFS && linux.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err := linux.SyncFS(mountPath)
		if err != nil {
			return false, fmt.Errorf("Failed syncing filesystem %q: %w", mountPath, err)
		}

		err = TryUnmount(mountPath, unix.MNT_DETACH)
		if err != nil {
			return false, err
		}

		d.logger.Debug("Unmounted TrueNAS volume", logger.Ctx{"volName": vol.name, "path": mountPath, "keepBlockDev": keepBlockDev})

		// And deactivate.
		if !keepBlockDev {
			_, err = d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}
		}

		ourUnmount = true
	} else if IsContentBlock(vol.contentType) {
		// For VMs, unmount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}

		if !keepBlockDev {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
				return false, ErrInUse
			}

			// and de-activate the block device
			_, err := d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}
		}
	}

	return ourUnmount, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *truenas) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newVolName, vol.config, vol.poolConfig)

	// Revert handling.
	reverter := revert.New()
	defer reverter.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolume(d, vol, newVolName, op)
	if err != nil {
		return err
	}

	reverter.Add(func() {
		_ = genericVFSRenameVolume(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	err = d.renameDataset(d.dataset(vol, false), d.dataset(newVol, false), true)
	if err != nil {
		return err
	}

	reverter.Add(func() {
		_ = d.renameDataset(d.dataset(newVol, false), d.dataset(vol, false), true)
	})

	// All done.
	reverter.Success()

	return nil
}

// MigrateVolume sends a volume for migration.
func (d *truenas) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *localMigration.VolumeSourceArgs, op *operations.Operation) error {
	if volSrcArgs.ClusterMove && !volSrcArgs.StorageMove {
		return nil // When performing a cluster member move don't do anything on the source member.
	}

	// Handle simple rsync and block_and_rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volSrcArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		// TODO this should take a temporary snapshot.
		// Before doing a generic volume migration, we need to ensure volume (or snap volume parent) is
		// activated to avoid issues activating the snapshot volume device.
		parent, _, _ := api.GetParentAndSnapshotName(vol.Name())
		parentVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, parent, vol.config, vol.poolConfig)
		err := d.MountVolume(parentVol, op)
		if err != nil {
			return err
		}

		defer func() { _, _ = d.UnmountVolume(parentVol, false, op) }()

		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	}

	// TODO: optimized migration between TrueNAS or ZFS storage pools?

	return ErrNotSupported
}

// BackupVolume creates an exported version of a volume.
func (d *truenas) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	// TODO: we should take a snapshot, and backup from the snapshot for consistency.
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *truenas) CreateVolumeSnapshot(vol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)

	// Revert handling.
	reverter := revert.New()
	defer reverter.Fail()

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, vol.volType, parentName)
	if err != nil {
		return err
	}

	// Create snapshot directory.
	err = vol.EnsureMountPath(false)
	if err != nil {
		return err
	}

	// Sync the filesystem
	if vol.contentType == ContentTypeFS {
		/*
			We want to ensure the current state is flushed to the server before snapping.

			Although Incus will Freeze Instances and VMs before Snapshot, then perform a SyncFS on the rootfs,
			that is only when going via CreateInstanceSnapshot, ie a Custom Volume will miss out as that goes
			via CreateCustomVolumeSnapshot, and there is no SyncFS.

			We may as well just sync any mounted filesystem, and if its already been synced there shouldn't be
			too many changes to flush to the server.

			In theory, a similar problem can exist with raw devices... and we may want to look at using something
			similar to `blockdev --flushbufs` to flush the block device before the snap.
		*/
		volMountPath := GetVolumeMountPath(vol.pool, vol.volType, parentName)
		if linux.IsMountPoint(volMountPath) {
			err := linux.SyncFS(volMountPath)
			if err != nil {
				return fmt.Errorf("Failed syncing filesystem %q: %w", volMountPath, err)
			}
		}
	}

	snapDataset := d.dataset(vol, false)

	// Sync the device. It may not be enough to just sync the mountpoint... because the device may not be mounted.
	parentDataset, _, ok := strings.Cut(snapDataset, "@")
	if ok {
		devPath, err := d.locateIscsiDataset(parentDataset)
		if err == nil && devPath != "" {
			err := linux.SyncFS(devPath)
			if err != nil {
				return fmt.Errorf("Failed syncing device %q: %w", devPath, err)
			}
		}
	}

	// Make the snapshot.
	err = d.createSnapshot(snapDataset, false)
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = d.DeleteVolumeSnapshot(vol, op) })

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = d.DeleteVolumeSnapshot(fsVol, op) })
	}

	// All done.
	reverter.Success()

	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *truenas) DeleteVolumeSnapshot(vol Volume, op *operations.Operation) error {
	// Delete the snapshot, which will fail if there are clones.
	dataset := d.dataset(vol, false)
	errDelete := d.deleteSnapshot(dataset, true)

	if errDelete != nil {
		// Handle clones.
		clones, err := d.getClones(dataset)
		if err != nil {
			return err
		}

		if len(clones) == 0 {
			return errDelete
		}

		// Move to the deleted path.
		err = d.renameSnapshot(dataset, d.dataset(vol, true))
		if err != nil {
			return err
		}
	}

	// Delete the mountpoint.
	err := os.Remove(vol.MountPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed to remove '%s': %w", vol.MountPath(), err)
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)
	err = deleteParentSnapshotDirIfEmpty(d.name, vol.volType, parentName)
	if err != nil {
		return err
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountVolumeSnapshot mounts a storage volume snapshot.
//
// The snapshot is cloned to a temporary dataset that will live for the duration of the mount.
func (d *truenas) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": snapVol.Name()})
	l.Debug("Mounting snapshot volume")

	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	reverter := revert.New()
	defer reverter.Fail()

	// For VMs, mount the filesystem volume.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		l.Debug("Created a new FS volume", logger.Ctx{"fsVol": fsVol})
		return d.MountVolumeSnapshot(fsVol, op)
	}

	srcSnapshot := d.dataset(snapVol, false)
	cloneDataset := d.getTempSnapshotVolName(snapVol)

	// Create a temporary clone from the snapshot.
	err = d.cloneSnapshot(srcSnapshot, cloneDataset)
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = d.deleteDatasetRecursive(cloneDataset) })

	// and share the clone
	err = d.createIscsiShare(cloneDataset, snapVol.contentType != ContentTypeFS) // ro if not FS
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = d.deleteIscsiShare(cloneDataset) })

	// and then activate
	// volDevPath, err := d.activateIscsiDataset(cloneDataset)
	_, volDevPath, err := d.locateOrActivateIscsiDataset(cloneDataset)
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = d.deactivateIscsiDataset(cloneDataset) })

	if snapVol.contentType == ContentTypeFS {
		mountPath := snapVol.MountPath()
		l.Debug("Content type FS", logger.Ctx{"mountPath": mountPath})
		if !linux.IsMountPoint(mountPath) {
			err := snapVol.EnsureMountPath(false)
			if err != nil {
				return err
			}

			snapVolFS := snapVol.ConfigBlockFilesystem()

			if snapVol.mountFilesystemProbe {
				snapVolFS, err = fsProbe(volDevPath)
				if err != nil {
					return fmt.Errorf("Failed probing filesystem: %w", err)
				}
			}

			mountFlags, mountOptions := linux.ResolveMountOptions(strings.Split(snapVol.ConfigBlockMountOptions(), ","))

			l.Debug("Regenerating filesystem UUID", logger.Ctx{"volDevPath": volDevPath, "fs": snapVolFS})
			if renegerateFilesystemUUIDNeeded(snapVolFS) {
				if snapVolFS == "xfs" {
					idx := strings.Index(mountOptions, "nouuid")
					if idx < 0 {
						mountOptions += ",nouuid"
					}
				} else {
					err = regenerateFilesystemUUID(snapVolFS, volDevPath)
					if err != nil {
						return err
					}
				}
			}

			l.Debug("Will try mount")
			err = TryMount(volDevPath, mountPath, snapVolFS, mountFlags, mountOptions)
			if err != nil {
				l.Debug("Tried mounting but failed", logger.Ctx{"error": err})
				return err
			}

			l.Debug("Mounted TrueNAS snapshot volume", logger.Ctx{"volName": snapVol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	reverter.Success()
	return nil
}

// UnmountVolumeSnapshot unmounts a volume snapshot.
//
// Will delete the temporary TrueNAS snapshot clone.
func (d *truenas) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	l := d.logger.AddContext(logger.Ctx{"volume": snapVol.Name()})
	l.Debug("Umounting TrueNAS snapshot volume", logger.Ctx{"vol": snapVol})

	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	// For VMs, unmount the filesystem volume.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		return d.UnmountVolumeSnapshot(fsVol, op)
	}

	ourUnmount := false
	mountPath := snapVol.MountPath()
	refCount := snapVol.MountRefCountDecrement()

	// Attempt to unmount the filesystem
	if snapVol.contentType == ContentTypeFS && linux.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err := linux.SyncFS(mountPath)
		if err != nil {
			return false, fmt.Errorf("Failed syncing filesystem %q: %w", mountPath, err)
		}

		ourUnmount, err = forceUnmount(mountPath)
		if err != nil {
			return false, err
		}

		l.Debug("Unmounted TrueNAS snapshot volume filesystem", logger.Ctx{"vol": snapVol, "path": mountPath})
	}

	cloneDataset := d.getTempSnapshotVolName(snapVol)

	l.Debug("Deleting temporary TrueNAS snapshot volume")

	// Deactivate & Delete iSCSI share
	err = d.deleteIscsiShare(cloneDataset)
	if err != nil {
		return false, fmt.Errorf("Could not delete iscsi target for temporary snapshot volume: %w", err)
	}

	// Destroy clone
	err = d.deleteDatasetRecursive(cloneDataset)
	if err != nil {
		return false, fmt.Errorf("Could not delete temporary snapshot volume: %w", err)
	}

	l.Debug("Temporary TrueNAS snapshot volume deleted")

	return ourUnmount, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *truenas) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	// Get all children datasets.
	dataset := d.dataset(vol, false)
	entries, err := d.getDatasets(dataset, "snapshot")
	if err != nil {
		return nil, err
	}

	// Filter only the snapshots.
	snapshots := []string{}
	for _, entry := range entries {
		after, ok := strings.CutPrefix(entry, "@snapshot-")
		if ok {
			snapshots = append(snapshots, after)
		}
	}

	return snapshots, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *truenas) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	return d.restoreVolume(vol, snapshotName, false, op)
}

func (d *truenas) restoreVolume(vol Volume, snapshotName string, isMigration bool, op *operations.Operation) error {
	// Get the list of snapshots.
	dataset := d.dataset(vol, false)
	entries, err := d.getDatasets(dataset, "snapshot")
	if err != nil {
		return err
	}

	// Check if more recent snapshots exist.
	idx := -1
	snapshots := []string{}
	for i, entry := range entries {
		if entry == fmt.Sprintf("@snapshot-%s", snapshotName) {
			// Located the current snapshot.
			idx = i
			continue
		} else if idx < 0 {
			// Skip any previous snapshot.
			continue
		}

		after, ok := strings.CutPrefix(entry, "@snapshot-")
		if ok {
			// Located a normal snapshot following ours.
			snapshots = append(snapshots, after)
			continue
		}

		if strings.HasPrefix(entry, "@") {
			// Located an internal snapshot.
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent internal snapshot(s) (from a copy)", snapshotName)
		}
	}

	// Check if snapshot removal is allowed.
	if len(snapshots) > 0 {
		if util.IsFalseOrEmpty(vol.ExpandedConfig("truenas.remove_snapshots")) {
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent snapshot(s). Set truenas.remove_snapshots to override", snapshotName)
		}

		// Setup custom error to tell the backend what to delete.
		err := ErrDeleteSnapshots{}
		err.Snapshots = snapshots
		return err
	}

	// TODO: this looks like its manually performing the repeated rollback. We should be able to ask middle to do this for us, the trick
	// is just to verify its good to go, which I think is the case after the above check. ie --recursive

	// Restore the snapshot.
	datasets, err := d.getDatasets(dataset, "snapshot")
	if err != nil {
		return err
	}

	toRollback := make([]string, 0)
	for _, dataset := range datasets {
		if !strings.HasSuffix(dataset, fmt.Sprintf("@snapshot-%s", snapshotName)) {
			continue
		}

		toRollback = append(toRollback, fmt.Sprintf("%s%s", d.dataset(vol, false), dataset))
	}

	if len(toRollback) > 0 {
		snapRbCmd := []string{"snapshot", "rollback"}
		_, err = d.runTool(append(snapRbCmd, toRollback...)...)
		if err != nil {
			return err
		}
	}

	if vol.contentType == ContentTypeFS && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
		_, _, err = d.activateVolume(vol)
		if err != nil {
			return err
		}

		defer func() { _, _ = d.deactivateVolume(vol) }()

		volPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return err
		}

		d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volPath, "fs": vol.ConfigBlockFilesystem()})
		err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volPath)
		if err != nil {
			return err
		}
	}

	// For VM images, restore the associated filesystem dataset too.
	if !isMigration && vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.restoreVolume(fsVol, snapshotName, isMigration, op)
		if err != nil {
			return err
		}
	}

	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *truenas) RenameVolumeSnapshot(vol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(vol.name)
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, fmt.Sprintf("%s/%s", parentName, newSnapshotName), vol.config, vol.poolConfig)

	// Revert handling.
	reverter := revert.New()
	defer reverter.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolumeSnapshot(d, vol, newSnapshotName, op)
	if err != nil {
		return err
	}

	reverter.Add(func() {
		_ = genericVFSRenameVolumeSnapshot(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	err = d.renameSnapshot(d.dataset(vol, false), d.dataset(newVol, false))
	if err != nil {
		return err
	}

	reverter.Add(func() {
		_ = d.renameSnapshot(d.dataset(newVol, false), d.dataset(vol, false))
	})

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.RenameVolumeSnapshot(fsVol, newSnapshotName, op)
		if err != nil {
			return err
		}

		reverter.Add(func() {
			newFsVol := NewVolume(d, d.name, newVol.volType, ContentTypeFS, newVol.name, newVol.config, newVol.poolConfig)
			_ = d.RenameVolumeSnapshot(newFsVol, vol.name, op)
		})
	}

	// All done.
	reverter.Success()

	return nil
}

// FillVolumeConfig populate volume with default config.
func (d *truenas) FillVolumeConfig(vol Volume) error {
	var excludedKeys []string

	// Copy volume.* configuration options from pool.
	// If vol has a source, ignore the block mode related config keys from the pool.
	if vol.hasSource || vol.IsVMBlock() || vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		excludedKeys = []string{"block.filesystem", "block.mount_options"}
	}

	// Copy volume.* configuration options from pool.
	// Exclude 'block.filesystem' and 'block.mount_options'
	// as this ones are handled below in this function and depends from volume type
	err := d.fillVolumeConfig(&vol, excludedKeys...)
	if err != nil {
		return err
	}

	// Only validate filesystem config keys for filesystem volumes or VM block volumes (which have an
	// associated filesystem volume).

	if vol.ContentType() == ContentTypeFS {
		// Inherit filesystem from pool if not set.
		if vol.config["block.filesystem"] == "" {
			vol.config["block.filesystem"] = d.config["volume.block.filesystem"]
		}

		// Default filesystem if neither volume nor pool specify an override.
		if vol.config["block.filesystem"] == "" {
			// Unchangeable volume property: Set unconditionally.
			vol.config["block.filesystem"] = DefaultFilesystem
		}

		// Inherit filesystem mount options from pool if not set.
		if vol.config["block.mount_options"] == "" {
			vol.config["block.mount_options"] = d.config["volume.block.mount_options"]
		}

		// Default filesystem mount options if neither volume nor pool specify an override.
		if vol.config["block.mount_options"] == "" {
			// Unchangeable volume property: Set unconditionally.
			vol.config["block.mount_options"] = "discard"
		}
	}

	return nil
}
