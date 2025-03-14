package drivers

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
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
	"golang.org/x/sys/unix"
)

// ContentTypeRootImg implies the filesystem contains a root.img which itself contains a filesystem
const ContentTypeFsImg = ContentType("fs-img")

func blockifyMountPath(vol *Volume) {
	vol.mountCustomPath = ""

	if vol.IsSnapshot() {
		deSnap := vol.Clone()
		parentName, snapName, _ := api.GetParentAndSnapshotName(deSnap.name)
		deSnap.name = fmt.Sprintf("%s.block/%s", parentName, snapName)
		vol.mountCustomPath = deSnap.MountPath()
	} else {
		vol.mountCustomPath = vol.MountPath() + ".block"
	}
}

// returns a clone of the img, but margked as an fs-img
func cloneVolAsFsImgVol(vol Volume) Volume {
	fsImgVol := vol.Clone()
	blockifyMountPath(&fsImgVol)
	fsImgVol.contentType = ContentTypeFsImg

	return fsImgVol
}

func isFsImgVol(vol Volume) bool {
	/*
		we need a third volume type so that we can tell the difference between an
		image:fs and an image:block, adn the image:block's config mount.

		Additionally, to mount the backing image for an image:fs, we need to use a
		different contentType to obtain a separate lock, without using block (see above)
	*/
	return vol.contentType == ContentTypeFsImg

}

func needsFsImgVol(vol Volume) bool {
	/*
		does the volume need an underlying FsImgVol

		the trick is to make sure that Images etc that aren't created via NewVMBlockFilesystemVolume
		are marked as loop-vols, where as NewVMBlockFilesystemVolume must not be.

		This is accomplished by ensuring that block.filesistem is applied in FillVolumeConfig
	*/
	return vol.contentType == ContentTypeFS && vol.config["block.filesystem"] != ""
}

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *truenas) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {

	// Revert handling
	revert := revert.New()
	defer revert.Fail()

	// must mount VM.block so we can access the root.img, as well as the config filesystem
	if vol.IsVMBlock() { // or fs-img...
		blockifyMountPath(&vol)
	}
	if vol.IsCustomBlock() {
		vol.mountCustomPath = "" // we have to override the mount path in CopyFromBackup, need to un-override here.
	}

	// Create mountpoint.
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.Remove(vol.MountPath()) })

	// Look for previously deleted images. (don't look for underlying, or we'll look after we've looked)
	if vol.volType == VolumeTypeImage && !isFsImgVol(vol) {
		dataset := d.dataset(vol, true)
		exists, err := d.datasetExists(dataset)
		if err != nil {
			return err
		}

		if exists {
			canRestore := true

			if vol.IsBlockBacked() && (vol.contentType == ContentTypeBlock || d.isBlockBacked(vol)) {
				// For block volumes check if the cached image volume is larger than the current pool volume.size
				// setting (if so we won't be able to resize the snapshot to that the smaller size later).
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

					_, err := d.runTool("dataset", "rename", d.dataset(vol, true), d.dataset(randomVol, true))
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
			}

			// Restore the image.
			if canRestore {
				d.logger.Debug("Restoring previously deleted cached image volume", logger.Ctx{"fingerprint": vol.Name()})
				_, err := d.runTool("dataset", "rename", dataset, d.dataset(vol, false))
				if err != nil {
					return err
				}

				// After this point we have a restored image, so setup revert.
				revert.Add(func() { _ = d.DeleteVolume(vol, op) })

				if vol.IsVMBlock() {
					fsVol := vol.NewVMBlockFilesystemVolume()
					_, err := d.runTool("dataset", "rename", d.dataset(fsVol, true), d.dataset(fsVol, false))

					if err != nil {
						return err
					}

					// no need to revert.add here as we have succeeded
				}

				revert.Success()
				return nil
			}
		}
	}

	/*
		if we are creating a block_mode volume we start by creating a regular fs to host
	*/
	if needsFsImgVol(vol) { // ie create the fs-img
		/*
			by making an FS Block volume, we automatically create the root.img file and fill it out
			same as we do for a VM, which means we can now mount it too.
		*/
		fsImgVol := cloneVolAsFsImgVol(vol)

		// TODO: this relies on "isBlockBacked" for fs-img blockbacked vols.
		// Convert to bytes.
		fsImgVol.config["size"] = vol.ConfigSize()
		if fsImgVol.config["size"] == "" || fsImgVol.config["size"] == "0" {
			fsImgVol.config["size"] = DefaultBlockSize
		}

		fsImgFiller := &VolumeFiller{
			Fill: func(innerVol Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) {
				// Get filesystem.
				filesystem := vol.ConfigBlockFilesystem() // outer-vol.

				if vol.contentType == ContentTypeFS {
					_, err := makeFSType(rootBlockPath, filesystem, nil)
					if err != nil {
						return 0, err
					}
				}
				sizeBytes, err := units.ParseByteSizeString(innerVol.config["size"])
				if err != nil {
					return 0, err
				}
				// sizeBytes should be correct
				return sizeBytes, nil
			},
		}

		/*
			create volume will mount, create the image file, then call our filler, and unmount, and then we can take care of the mounting the side-car
			in MountVolume
		*/
		err = d.CreateVolume(fsImgVol, fsImgFiller, op)
		if err != nil {
			return err
		}

		// After this point we have a backing volume, so setup revert.
		revert.Add(func() { _ = d.DeleteVolume(fsImgVol, op) })
	}

	// for  block or fs-img we need to create a dataset
	if vol.contentType == ContentTypeBlock || isFsImgVol(vol) || (vol.contentType == ContentTypeFS && !needsFsImgVol(vol)) {

		/*
			for a VMBlock we need to create both a .block with an root.img and a filesystem
			volume. The filesystem volume has to be separate so that it can have a separate quota
			to the root.img/block volume.
		*/

		// Create the filesystem dataset.
		dataset := d.dataset(vol, false)
		err := d.createDataset(dataset) // TODO: we should set the filesystem on the dataset so that it can be recovered eventually in ListVolumes (and possibly mount options)
		if err != nil {
			return err
		}

		// Now have a dataset, so setup revert
		revert.Add(func() { _ = d.DeleteVolume(vol, op) })

		// now share it
		err = d.createNfsShare(dataset)
		if err != nil {
			return err
		}

		// Apply the size limit.
		err = d.SetVolumeQuota(vol, vol.ConfigSize(), false, op)
		if err != nil {
			return err
		}

		// Apply the blocksize.
		err = d.setBlocksizeFromConfig(vol)
		if err != nil {
			return err
		}
	}

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(fsVol, op) })
	}

	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {

		// path to disk volume if volume is block or iso.
		var rootBlockPath string

		// If we are creating a block volume, resize it to the requested size or the default.
		// For block volumes, we expect the filler function to have converted the qcow2 image to raw into the rootBlockPath.
		// For ISOs the content will just be copied.
		if IsContentBlock(vol.contentType) || isFsImgVol(vol) {

			// TODO: this relies on "isBlockBacked" for fs-img blockbacked vols.
			// Convert to bytes.
			sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
			if err != nil {
				return err
			}

			// We expect the filler to copy the VM image into this path.
			rootBlockPath, err = d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			// Ignore ErrCannotBeShrunk when setting size this just means the filler run above has needed to
			// increase the volume size beyond the default block volume size.
			_, err = ensureVolumeBlockFile(vol, rootBlockPath, sizeBytes, false)
			if err != nil && !errors.Is(err, ErrCannotBeShrunk) {
				return err
			}
		}

		// Run the volume filler function if supplied.
		if filler != nil && filler.Fill != nil {
			var err error

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
			if vol.IsVMBlock() { // can do for FS-IMG too
				vol.mountCustomPath = "" // need to zap path... or the filler will fill into the .block path...
				// the fillter expects the metadata volume to passed in.
			}
			err = d.runFiller(vol, rootBlockPath, filler, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Move the GPT alt header to end of disk if needed.
			if vol.IsVMBlock() { // TODO: this will corrupt our image that we lay down.
				err = d.moveGPTAltHeader(rootBlockPath)
				if err != nil {
					return err
				}
			}
		}

		//if vol.contentType == ContentTypeFS {
		// Run EnsureMountPath again after mounting and filling to ensure the mount directory has
		// the correct permissions set.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}
		//}

		return nil
	}, op)
	if err != nil {
		return err
	}

	// Setup snapshot and unset mountpoint on image.
	if vol.volType == VolumeTypeImage && !isFsImgVol(vol) {

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
	revert.Success()

	return nil
}

// CreateVolumeFromBackup re-creates a volume from its exported state.
func (d *truenas) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	// TODO: optimized version

	// if vol.IsCustomBlock() {
	// 	vol.mountCustomPath = fmt.Sprintf("%s/root.img", vol.MountPath())
	// }

	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// same as CreateVolumeFromCopy, but will refresh if refresh is true.
func (d *truenas) createOrRefeshVolumeFromCopy(vol Volume, srcVol Volume, refresh bool, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	var err error

	// Revert handling
	revert := revert.New()
	defer revert.Fail()

	// must mount VM.block so we can access the root.img, as well as the config filesystem
	if vol.IsVMBlock() {
		blockifyMountPath(&vol)
		blockifyMountPath(&srcVol)
	}

	//if vol.contentType == ContentTypeFS {
	// Create mountpoint.
	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.Remove(vol.MountPath()) })
	//}

	if needsFsImgVol(vol) { // ie create the fs-img
		/*
			by making an FS Block volume, we automatically create the root.img file and fill it out
			same as we do for a VM, which means we can now mount it too.
		*/
		fsImgVol := cloneVolAsFsImgVol(vol)
		err = fsImgVol.EnsureMountPath()
		if err != nil {
			return err
		}
		revert.Add(func() { _ = os.Remove(fsImgVol.MountPath()) })
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
			revert.Add(func() { _ = d.DeleteVolume(fsVol, op) })
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
	// consistent by syncing. Ideally we'd freeze the fs too.
	sourcePath := srcVol.MountPath()
	if !allowInconsistent && linux.IsMountPoint(sourcePath) {
		/*
			The Instance was already frozen if it were running. Incus tries to flush the filesystem, but
			it only flushes lxc rootfs directories. We need to separately flush the whole NFS mount which
			contains the root.img if applicable.
		*/
		err := linux.SyncFS(sourcePath)
		if err != nil {
			return fmt.Errorf("Failed syncing filesystem %q: %w", sourcePath, err)
		}
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
				out, err := d.runTool("snapshot", "delete", "-r", "--defer", srcSnapshot)
				_ = out

				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			}()
		} else {
			// Delete the snapshot on revert.
			revert.Add(func() {
				// Delete snapshot (or mark for deferred deletion if cannot be deleted currently).
				out, err := d.runTool("snapshot", "delete", "-r", "--defer", srcSnapshot)
				_ = out
				if err != nil {
					d.logger.Warn("Failed deleting temporary snapshot for copy", logger.Ctx{"snapshot": srcSnapshot, "err": err})
				}
			})
		}
	}

	// Delete the volume created on failure.
	if !refresh {
		revert.Add(func() { _ = d.DeleteVolume(vol, op) })
	}

	destDataset := d.dataset(vol, false)

	// If truenas.clone_copy is disabled or source volume has snapshots, then use full copy mode.
	if util.IsFalse(d.config["truenas.clone_copy"]) || len(snapshots) > 0 {
		/*
		 instead of using full replication, and then removing snapshots, we instead take advantage of the replication task's
		 ability to filter snapshots as they are sent.
		*/
		snapName := strings.SplitN(srcSnapshot, "@", 2)[1]
		snapRegex := fmt.Sprintf("(snapshot-.*|%s)", snapName)

		// Run the replication, snaps + copy- snap. TODO: verify necessary props are replicated.
		args := []string{"replication", "start", "--name-regex", snapRegex, "-r"}

		options := "readonly=IGNORE"
		if refresh {
			// refresh implies that we have a dest already, and that we want to re-replicate from scratch
			args = append(args, "--retention-policy=source")
			options += ",allow_from_scratch=true"
		}

		args = append(args, "-o", options, srcDataset, destDataset)

		_, err := d.runTool(args...)
		if err != nil {
			return fmt.Errorf("Failed to replicate dataset: %w", err)
		}

		// Delete the copy- snapshot on the dest.
		_, err = d.runTool("snapshot", "delete", "-r", fmt.Sprintf("%s@%s", destDataset, snapName))
		if err != nil {
			return err
		}
	} else {
		// Perform volume clone.
		args := []string{"snapshot", "clone"}
		args = append(args, srcSnapshot, destDataset)

		// Clone the snapshot.
		out, err := d.runTool(args...)
		_ = out

		if err != nil {
			return err
		}

	}

	// and share the clone/copy.
	err = d.createNfsShare(destDataset)
	if err != nil {
		return err
	}

	// Apply the properties.
	if vol.contentType == ContentTypeFS {
		if !d.isBlockBacked(srcVol) {

			// err := d.setDatasetProperties(d.dataset(vol, false), "mountpoint=legacy", "canmount=noauto")
			// if err != nil {
			// 	return err
			// }

			// Apply the blocksize.
			err = d.setBlocksizeFromConfig(vol)
			if err != nil {
				return err
			}
		}

		// would be better to mount where below does "activate"

		// Mounts the volume and ensure the permissions are set correctly inside the mounted volume.
		verifyVolMountPath := func() error {
			return vol.MountTask(func(_ string, _ *operations.Operation) error {
				return vol.EnsureMountPath()
			}, op)
		}

		if d.isBlockBacked(srcVol) && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {

			// regen must be done with vol unmounted.

			// _, err := d.activateVolume(vol)
			// if err != nil {
			// 	return err
			// }

			// TODO: to do this we need to mount the fs-img.
			fsImgVol := cloneVolAsFsImgVol(vol)
			err := fsImgVol.MountTask(func(mountPath string, op *operations.Operation) error {

				rootBlockPath, err := d.GetVolumeDiskPath(fsImgVol)
				if err != nil {
					return err
				}

				d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": rootBlockPath, "fs": vol.ConfigBlockFilesystem()})
				err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), rootBlockPath)
				if err != nil {
					return err
				}

				// performing the mount while the fsImg is mounted prevents repeated remounts.
				err = verifyVolMountPath()
				if err != nil {
					return err
				}

				return nil
			}, op)

			if err != nil {
				return err
			}

		} else {
			// Mount the volume and ensure the permissions are set correctly inside the mounted volume.
			err = verifyVolMountPath()
			if err != nil {
				return err
			}
		}
	}

	// Pass allowUnsafeResize as true when resizing block backed filesystem volumes because we want to allow
	// the filesystem to be shrunk as small as possible without needing the safety checks that would prevent
	// leaving the filesystem in an inconsistent state if the resize couldn't be completed. This is because if
	// the resize fails we will delete the volume anyway so don't have to worry about it being inconsistent.
	var allowUnsafeResize bool
	_ = allowUnsafeResize

	if d.isBlockBacked(vol) && vol.contentType == ContentTypeFS {
		allowUnsafeResize = true
	}

	// Resize volume to the size specified. Only uses volume "size" property and does not use pool/defaults
	// to give the caller more control over the size being used.
	err = d.SetVolumeQuota(vol, vol.config["size"], allowUnsafeResize, op)
	if err != nil {
		return err
	}

	// All done.
	revert.Success()
	return nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *truenas) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	return d.createOrRefeshVolumeFromCopy(vol, srcVol, false, copySnapshots, allowInconsistent, op) // not refreshing.
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *truenas) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs localMigration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if volTargetArgs.ClusterMoveSourceName != "" && volTargetArgs.StoragePool == "" {
		err := vol.EnsureMountPath()
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
	/*
		The below code essentially tries deleting the volume with the various filesystems,
		but since we don't use the suffix, except when an image is already deleted, we don't
		need to check the suffixes.

		The suffix can be tested by grabbing an image, and then changing the volume filesytem
		which will cause a different image to be created, and "delete" the old one. The deleted
		image will then be deleted when its last clone is removed.
	*/
	// if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS {
	// 	// We need to clone vol the otherwise changing `zfs.block_mode`
	// 	// in tmpVol will also change it in vol.
	// 	tmpVol := vol.Clone()

	// 	for _, filesystem := range blockBackedAllowedFilesystems {
	// 		tmpVol.config["block.filesystem"] = filesystem

	// 		err := d.deleteVolume(tmpVol, op)
	// 		if err != nil {
	// 			return err
	// 		}
	// 	}
	// }

	return d.deleteVolume(vol, op)
}

func (d *truenas) deleteVolume(vol Volume, op *operations.Operation) error {

	// Check that we have a dataset to delete.
	dataset := d.dataset(vol, false)
	exists, err := d.datasetExists(dataset)
	if err != nil {
		return err
	}

	if exists {
		// Handle clones.
		clones, err := d.getClones(dataset)
		if err != nil {
			return err
		}

		if len(clones) > 0 {
			// Deleted volumes do not need shares
			_ = d.deleteNfsShare(dataset)

			// Move to the deleted path.
			out, err := d.renameDataset(dataset, d.dataset(vol, true), false)
			_ = out
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

	// must mount VM.block so we can access the root.img, as well as the config filesystem
	if vol.IsVMBlock() {
		blockifyMountPath(&vol)
	}

	// Delete the mountpoint if present.
	err = os.Remove(vol.MountPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed to remove '%s': %w", vol.MountPath(), err)
	}

	if vol.contentType == ContentTypeFS {
		// Delete the snapshot storage.
		err = os.RemoveAll(GetVolumeSnapshotDir(d.name, vol.volType, vol.name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", GetVolumeSnapshotDir(d.name, vol.volType, vol.name), err)
		}

		// TODO: we should probably cleanup using DeleteVolume.
		if needsFsImgVol(vol) {
			fsImgVol := cloneVolAsFsImgVol(vol)
			err := os.Remove(fsImgVol.MountPath())
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("Failed to remove '%s': %w", fsImgVol.MountPath(), err)
			}
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

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *truenas) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		"block.filesystem":     validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		"block.mount_options":  validate.IsAny,
		"truenas.block_mode":   validate.Optional(validate.IsBool),
		"zfs.blocksize":        validate.Optional(ValidateZfsBlocksize),
		"zfs.remove_snapshots": validate.Optional(validate.IsBool),
		"zfs.reserve_space":    validate.Optional(validate.IsBool),
		"zfs.use_refquota":     validate.Optional(validate.IsBool),
		"zfs.delegate":         validate.Optional(validate.IsBool),
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

// // UpdateVolume applies config changes to the volume.
func (d *truenas) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	// Mangle the current volume to its old values.
	old := make(map[string]string)
	for k, v := range changedConfig {
		if k == "size" || k == "zfs.use_refquota" || k == "zfs.reserve_space" {
			old[k] = vol.config[k]
			vol.config[k] = v
		}

		if k == "zfs.blocksize" {
			// Convert to bytes.
			sizeBytes, err := units.ParseByteSizeString(v)
			if err != nil {
				return err
			}

			err = d.setBlocksize(vol, sizeBytes)
			if err != nil {
				return err
			}
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

// // GetVolumeUsage returns the disk space used by the volume.
// func (d *zfs) GetVolumeUsage(vol Volume) (int64, error) {
// 	// Determine what key to use.
// 	key := "used"

// 	// If volume isn't snapshot then we can take into account the zfs.use_refquota setting.
// 	// Snapshots should also use the "used" ZFS property because the snapshot usage size represents the CoW
// 	// usage not the size of the snapshot volume.
// 	if !vol.IsSnapshot() {
// 		if util.IsTrue(vol.ExpandedConfig("zfs.use_refquota")) {
// 			key = "referenced"
// 		}

// 		// Shortcut for mounted refquota filesystems.
// 		if key == "referenced" && vol.contentType == ContentTypeFS && linux.IsMountPoint(vol.MountPath()) {
// 			var stat unix.Statfs_t
// 			err := unix.Statfs(vol.MountPath(), &stat)
// 			if err != nil {
// 				return -1, err
// 			}

// 			return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
// 		}
// 	}

// 	// Get the current value.
// 	value, err := d.getDatasetProperty(d.dataset(vol, false), key)
// 	if err != nil {
// 		return -1, err
// 	}

// 	// Convert to int.
// 	valueInt, err := strconv.ParseInt(value, 10, 64)
// 	if err != nil {
// 		return -1, err
// 	}

// 	return valueInt, nil
// }

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size for block volumes, and for filesystem volumes removes quota.
func (d *truenas) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	// For VM block files, resize the file if needed.
	if vol.IsBlockBacked() || vol.IsCustomBlock() || vol.IsVMBlock() {

		if vol.IsBlockBacked() && vol.MountInUse() {
			return ErrInUse // We don't allow online resizing of block volumes.
		}

		/*
			we want to resize the root.img in the vol, but if we simply mount the vol
			then ensureVolumeBlockFile function will reject a resize of an online vol, but
			an fs-img vol counts as a separate mount-lock
		*/
		// fsImgVol := cloneVolAsFsImgVol(vol)
		// if vol.IsCustomBlock() {
		// 	fsImgVol.mountCustomPath = "" // deblockify, no need to create a secondary mount point
		// }

		err := vol.MountTask(func(mountPath string, op *operations.Operation) error {

			// We expect the filler to copy the VM image into this path.
			rootBlockPath, err := d.GetVolumeDiskPath(vol)
			if err != nil {
				return err
			}

			_, err = ensureVolumeBlockFile(vol, rootBlockPath, sizeBytes, allowUnsafeResize)
			if err == ErrInUse {
				/*
					this error is expected because we have the vol mounted...
					but now that we've passed the size check we can perform an unsafe
					resize to bypass the mounted check
				*/
				_, err = ensureVolumeBlockFile(vol, rootBlockPath, sizeBytes, true)
			}
			if err != nil {
				return err
			}

			// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as
			// it is expected the caller will do all necessary post resize actions themselves).
			if vol.IsVMBlock() && !allowUnsafeResize {
				err = d.moveGPTAltHeader(rootBlockPath)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}

	} else if vol.Type() != VolumeTypeBucket {
		// For non-VM block volumes, set filesystem quota.
		volID, err := d.getVolID(vol.volType, vol.name)
		_ = volID
		if err != nil {
			return err
		}

		// Custom handling for filesystem volume associated with a VM, if the file is in the state dataaset. which its not currently
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

		//return d.setQuota(vol.MountPath(), volID, sizeBytes)
		dataset := d.dataset(vol, false)
		return d.setDatasetQuota(dataset, sizeBytes)
	}

	return nil
}

// se: from driver_dir_volumes.go
// GetVolumeDiskPath returns the location of a disk volume.
func (d *truenas) GetVolumeDiskPath(vol Volume) (string, error) {

	if vol.IsVMBlock() { // or FS-IMG
		blockifyMountPath(&vol) // when backend calls GetVolumeDiskPath, it needs to refer to the .block mount.
	}
	if vol.IsCustomBlock() {
		vol.mountCustomPath = ""
	}

	return filepath.Join(vol.MountPath(), genericVolumeDiskFile), nil
}

// ListVolumes returns a list of volumes in storage pool.
func (d *truenas) ListVolumes() ([]Volume, error) {
	vols := make(map[string]Volume)
	_ = vols

	// Get just filesystem and volume datasets, not snapshots.
	// The ZFS driver uses two approaches to indicating block volumes; firstly for VM and image volumes it
	// creates both a filesystem dataset and an associated volume ending in zfsBlockVolSuffix.
	// However for custom block volumes it does not also end the volume name in zfsBlockVolSuffix (unlike the
	// LVM and Ceph drivers), so we must also retrieve the dataset type here and look for "volume" types
	// which also indicate this is a block volume.
	out, err := d.runTool("list", "-H", "-o", "name,type,incus:content_type", "-r", "-t", "filesystem,volume", d.config["truenas.dataset"])
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(out))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Splitting fields on tab should be safe as ZFS doesn't appear to allow tabs in dataset names.
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("Unexpected volume line %q", line)
		}

		zfsVolName := parts[0]
		zfsContentType := parts[1]
		incusContentType := parts[2]

		var volType VolumeType
		var volName string

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

		// Detect if a volume is block content type using only the dataset type.
		isBlock := zfsContentType == "volume"

		if volType == VolumeTypeVM && !isBlock {
			continue // Ignore VM filesystem volumes as we will just return the VM's block volume.
		}

		contentType := ContentTypeFS
		if isBlock {
			contentType = ContentTypeBlock
		}

		if volType == VolumeTypeCustom && isBlock && strings.HasSuffix(volName, zfsISOVolSuffix) {
			contentType = ContentTypeISO
			volName = strings.TrimSuffix(volName, zfsISOVolSuffix)
		} else if volType == VolumeTypeVM || isBlock {
			volName = strings.TrimSuffix(volName, zfsBlockVolSuffix)
		}

		// If a new volume has been found, or the volume will replace an existing image filesystem volume
		// then proceed to add the volume to the map. We allow image volumes to overwrite existing
		// filesystem volumes of the same name so that for VM images we only return the block content type
		// volume (so that only the single "logical" volume is returned).
		existingVol, foundExisting := vols[volName]
		if !foundExisting || (existingVol.Type() == VolumeTypeImage && existingVol.ContentType() == ContentTypeFS) {
			v := NewVolume(d, d.name, volType, contentType, volName, make(map[string]string), d.config)

			if isBlock {
				// Get correct content type from incus:content_type property.
				if incusContentType != "-" {
					v.contentType = ContentType(incusContentType)
				}

				if v.contentType == ContentTypeBlock {
					v.SetMountFilesystemProbe(true)
				}
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

func (d *truenas) activateAndMountFsImg(vol Volume, op *operations.Operation) error {

	revert := revert.New()
	defer revert.Fail()

	// mount underlying dataset, then loop mount the root.img
	fsImgVol := cloneVolAsFsImgVol(vol)

	err := d.MountVolume(fsImgVol, op)
	if err != nil {
		return err
	}
	revert.Add(func() {
		_, _ = d.UnmountVolume(fsImgVol, false, op)
	})

	// We expect the filler to copy the VM image into this path.
	rootBlockPath, err := d.GetVolumeDiskPath(fsImgVol)
	if err != nil {
		return err
	}

	fsType, err := fsProbe(rootBlockPath)
	if err != nil {
		return fmt.Errorf("Failed probing filesystem: %w", err)
	}
	if fsType == "" {
		// if we couldn't probe it, we won't be able to mount it.
		return fmt.Errorf("Failed probing filesystem: %s", rootBlockPath)
	}

	loopDevPath, err := loopDeviceSetup(rootBlockPath)
	if err != nil {
		return err
	}
	revert.Add(func() {
		loopDeviceAutoDetach(loopDevPath)
	})

	mountPath := vol.MountPath()

	//var volOptions []string
	volOptions := strings.Split(vol.ConfigBlockMountOptions(), ",")

	mountFlags, mountOptions := linux.ResolveMountOptions(volOptions)
	_ = mountFlags
	err = TryMount(loopDevPath, mountPath, fsType, mountFlags, mountOptions)
	if err != nil {
		defer func() { _ = loopDeviceAutoDetach(loopDevPath) }()
		return err
	}
	d.logger.Debug("Mounted TrueNAS volume", logger.Ctx{"volName": vol.name, "dev": rootBlockPath, "path": mountPath, "options": mountOptions})

	revert.Success()

	return nil
}

func (d *truenas) mountNfsDataset(vol Volume) error {

	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	dataset := d.dataset(vol, false)

	var volOptions []string

	//note: to implement getDatasetProperties, we'd like `truenas-admin dataset inspect` to be implemented
	atime, err := d.getDatasetProperty(dataset, "atime")
	if err != nil {
		return err
	}
	if atime == "off" {
		volOptions = append(volOptions, "noatime")
	}

	host := d.config["truenas.host"]
	if host == "" {
		return fmt.Errorf("`truenas.host` must be specified")
	}

	ip4and6, err := net.LookupIP(host)
	if err != nil {
		return err
	}

	// NFS
	volOptions = append(volOptions, "vers=4.2")                  // TODO: decide on default options
	volOptions = append(volOptions, "addr="+ip4and6[0].String()) // TODO: pick ip4 or ip6

	mountFlags, mountOptions := linux.ResolveMountOptions(volOptions)
	mountPath := vol.MountPath()

	remotePath := fmt.Sprintf("%s:/mnt/%s", host, dataset)

	// Mount the dataset.
	err = TryMount(remotePath, mountPath, "nfs", mountFlags, mountOptions) // TODO: if local we want to bind mount.

	if err != nil {
		// try once more, after re-creating the share.
		err = d.createNfsShare(dataset)
		if err != nil {
			return err
		}
		err = TryMount(remotePath, mountPath, "nfs", mountFlags, mountOptions)
		if err != nil {
			return err
		}
	}

	d.logger.Debug("Mounted TrueNAS dataset", logger.Ctx{"volName": vol.name, "host": host, "dev": dataset, "path": mountPath})

	return nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *truenas) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	if vol.contentType == ContentTypeFS || isFsImgVol(vol) || vol.IsVMBlock() || vol.IsCustomBlock() {

		if vol.IsVMBlock() { // OR fs-img
			blockifyMountPath(&vol)
		}
		if vol.IsCustomBlock() {
			vol.mountCustomPath = "" // need to clear the customized mount path used for CreateFromBackup
		}

		// handle an FS mount

		mountPath := vol.MountPath()
		if !linux.IsMountPoint(mountPath) {

			if needsFsImgVol(vol) {

				// mount underlying fs, then create a loop device for the fs-img, and mount that
				err = d.activateAndMountFsImg(vol, op)
				if err != nil {
					return err
				}

			} else {

				// otherwise, we can just NFS mount a dataset
				err = d.mountNfsDataset(vol)
				if err != nil {
					return err
				}
			}
		}

	}

	// now, if we were a VM block we also need to mount the config filesystem
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err = d.MountVolume(fsVol, op)
		if err != nil {
			return err
		}
	} // PS: not 100% sure what to do about ISOs yet.

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	revert.Success()
	return nil
}

func (d *truenas) deactivateVolume(vol Volume, op *operations.Operation) (bool, error) {
	ourUnmount := true

	// need to unlink the loop
	// mount underlying dataset, then loop mount the root.img
	// we need to mount the underlying dataset
	fsImgVol := cloneVolAsFsImgVol(vol)

	// We expect the filler to copy the VM image into this path.
	rootBlockPath, err := d.GetVolumeDiskPath(fsImgVol)
	if err != nil {
		return false, err
	}
	loopDevPath, err := loopDeviceSetup(rootBlockPath)
	if err != nil {
		return false, err
	}
	err = loopDeviceAutoDetach(loopDevPath)
	if err != nil {
		return false, err
	}

	// and then unmount the root.img dataset

	_, err = d.UnmountVolume(fsImgVol, false, op)
	if err != nil {
		return false, err
	}

	return ourUnmount, nil
}

// UnmountVolume unmounts volume if mounted and not in use. Returns true if this unmounted the volume.
// keepBlockDev indicates if backing block device should be not be deactivated when volume is unmounted.
func (d *truenas) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	if vol.IsVMBlock() {
		blockifyMountPath(&vol)
	}

	ourUnmount := false
	dataset := d.dataset(vol, false)
	mountPath := vol.MountPath()

	refCount := vol.MountRefCountDecrement()

	if keepBlockDev {
		d.logger.Debug("keepBlockDevTrue", logger.Ctx{"volName": vol.name, "refCount": refCount})
	}

	if vol.contentType == ContentTypeBlock || vol.contentType == ContentTypeISO {
		// For VMs and ISOs, unmount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}
	}

	if refCount > 0 {
		d.logger.Debug("Skipping TrueNAS unmount as in use", logger.Ctx{"volName": vol.name, "host": d.config["truenas.host"], "dataset": dataset, "path": mountPath})
		return false, ErrInUse
	}

	if (vol.contentType == ContentTypeFS || vol.IsVMBlock() || vol.IsCustomBlock() || isFsImgVol(vol)) && linux.IsMountPoint(mountPath) {

		// Unmount the dataset.
		err = TryUnmount(mountPath, 0)
		if err != nil {
			return false, err
		}
		ourUnmount = true

		// if we're a loop mounted volume...
		if needsFsImgVol(vol) {

			// then we've unmounted the volume

			d.logger.Debug("Unmounted TrueNAS volume", logger.Ctx{"volName": vol.name, "host": d.config["truenas.host"], "dataset": dataset, "path": mountPath})

			// now we can take down the loop and the fs-img dataset
			_, err = d.deactivateVolume(vol, op)
			if err != nil {
				return false, err
			}

		} else {
			// otherwise, we're just a regular dataset mount.
			d.logger.Debug("Unmounted TrueNAS dataset", logger.Ctx{"volName": vol.name, "host": d.config["truenas.host"], "dataset": dataset, "path": mountPath})
		}

	}

	return ourUnmount, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *truenas) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	newVol := NewVolume(d, d.name, vol.volType, vol.contentType, newVolName, vol.config, vol.poolConfig)

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolume(d, vol, newVolName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = genericVFSRenameVolume(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	out, err := d.renameDataset(d.dataset(vol, false), d.dataset(newVol, false), true)
	_ = out
	if err != nil {
		return err
	}

	revert.Add(func() {
		_, _ = d.renameDataset(d.dataset(newVol, false), d.dataset(vol, false), true)

	})

	// All done.
	revert.Success()

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

	// TODO: optmized migration between TrueNAS or ZFS storage pools?

	return ErrNotSupported
}

// BackupVolume creates an exported version of a volume.
func (d *truenas) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	// TODO: we should take a snapshot, and backup from the snapshot for consistency.
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *truenas) CreateVolumeSnapshot(vol Volume, op *operations.Operation) error {

	origVol := vol.Clone() // when calling Create/Delete we need the original vol

	// must mount VM.block so we can access the root.img, as well as the config filesystem
	parentName, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	if vol.IsVMBlock() { // or fs-img...
		// for a VM, we need to prefix the name
		vol.name = fmt.Sprintf("%s.block/%s", parentName, snapName)
		parentName += ".block"
	}

	// Revert handling.
	revert := revert.New()
	defer revert.Fail()

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, vol.volType, parentName)
	if err != nil {
		return err
	}

	// Create snapshot directory.
	err = vol.EnsureMountPath()
	if err != nil {
		return err
	}

	if vol.IsVMBlock() {
		/*
			We want to ensure the current state is flushed to the server before snapping.

			Incus will Freeze the Instance before the snapshot, but if its a VM it won't Sync the FS
			correctly as it targets the ./rootfs as used by lxc

			In future, a better solution may be to correct the Freeze/Unfreeze logic to figure out to
			use the VM's filesystem.

			Ideally, this whole function needs to return ASAP so that the VM will be unfrozen ASAP
		*/
		volMountPath := GetVolumeMountPath(vol.pool, vol.volType, parentName)
		if linux.IsMountPoint(volMountPath) {
			err := linux.SyncFS(volMountPath)
			if err != nil {
				return fmt.Errorf("Failed syncing filesystem %q: %w", volMountPath, err)
			}
		}
	}

	// Make the snapshot.
	dataset := d.dataset(origVol, false)
	err = d.createSnapshot(dataset, false)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = d.DeleteVolumeSnapshot(origVol, op) })

	// For VM images, create a filesystem volume too.
	if vol.IsVMBlock() {

		fsVol := origVol.NewVMBlockFilesystemVolume()
		err := d.CreateVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolumeSnapshot(fsVol, op) })
	}

	// All done.
	revert.Success()

	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *truenas) DeleteVolumeSnapshot(vol Volume, op *operations.Operation) error {

	dataset := d.dataset(vol, false)
	// Handle clones.
	clones, err := d.getClones(dataset)
	if err != nil {
		return err
	}

	if len(clones) > 0 {
		// Move to the deleted path.
		deletedDataset := d.dataset(vol, true)
		out, err := d.renameSnapshot(dataset, deletedDataset)

		_ = out
		if err != nil {
			return err
		}
	} else {
		// Delete the snapshot.
		out, err := d.runTool("snapshot", "delete", "-r", dataset)
		_ = out
		if err != nil {
			return err
		}
	}

	// must mount VM.block so we can access the root.img, as well as the config filesystem
	parentName, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	mountPath := vol.MountPath()
	if vol.IsVMBlock() { // or fs-img...
		// for a VM, we need to prefix the name
		modVol := vol.Clone() // when calling Create/Delete we need the original vol
		modVol.name = fmt.Sprintf("%s.block/%s", parentName, snapName)
		mountPath = modVol.MountPath()
		parentName += ".block"
	}

	// Delete the mountpoint.
	err = os.Remove(mountPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed to remove '%s': %w", mountPath, err)
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
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

// MountVolumeSnapshot simulates mounting a volume snapshot.
func (d *truenas) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	_, err = d.mountVolumeSnapshot(snapVol, d.dataset(snapVol, false), snapVol.MountPath(), op)
	if err != nil {
		return err
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	return nil
}

func (d *truenas) mountVolumeSnapshot(snapVol Volume, snapshotDataset string, mountPath string, op *operations.Operation) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Check if filesystem volume already mounted.
	if snapVol.contentType == ContentTypeFS && !d.isBlockBacked(snapVol) {
		if !linux.IsMountPoint(mountPath) {
			err := snapVol.EnsureMountPath()
			if err != nil {
				return nil, err
			}

			// Mount the snapshot directly (not possible through tools).
			err = TryMount(snapshotDataset, mountPath, "zfs", unix.MS_RDONLY, "")
			if err != nil {
				return nil, err
			}

			d.logger.Debug("Mounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
		}
	} else {
		// snipped.
		return nil, fmt.Errorf("contentType == ContentTypeBlock not implemented")
	}

	d.logger.Debug("Mounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})

	revert.Add(func() {
		_, err := forceUnmount(mountPath)
		if err != nil {
			return
		}

		d.logger.Debug("Unmounted ZFS snapshot dataset", logger.Ctx{"dev": snapshotDataset, "path": mountPath})
	})

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
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
		if strings.HasPrefix(entry, "@snapshot-") {
			snapshots = append(snapshots, strings.TrimPrefix(entry, "@snapshot-"))
		}
	}

	return snapshots, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *truenas) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	return d.restoreVolume(vol, snapshotName, false, op)
}

func (d *truenas) restoreVolume(vol Volume, snapshotName string, migration bool, op *operations.Operation) error {
	// Get the list of snapshots.
	entries, err := d.getDatasets(d.dataset(vol, false), "snapshot")
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

		if strings.HasPrefix(entry, "@snapshot-") {
			// Located a normal snapshot following ours.
			snapshots = append(snapshots, strings.TrimPrefix(entry, "@snapshot-"))
			continue
		}

		if strings.HasPrefix(entry, "@") {
			// Located an internal snapshot.
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent internal snapshot(s) (from a copy)", snapshotName)
		}
	}

	// Check if snapshot removal is allowed.
	if len(snapshots) > 0 {
		if util.IsFalseOrEmpty(vol.ExpandedConfig("zfs.remove_snapshots")) {
			return fmt.Errorf("Snapshot %q cannot be restored due to subsequent snapshot(s). Set zfs.remove_snapshots to override", snapshotName)
		}

		// Setup custom error to tell the backend what to delete.
		err := ErrDeleteSnapshots{}
		err.Snapshots = snapshots
		return err
	}

	// Restore the snapshot.
	datasets, err := d.getDatasets(d.dataset(vol, false), "snapshot")
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

	if vol.contentType == ContentTypeFS && d.isBlockBacked(vol) && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
		// _, err = d.activateVolume(vol)
		// if err != nil {
		// 	return err
		// }

		//defer func() { _, _ = d.deactivateVolume(vol) }()

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
	if !migration && vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.restoreVolume(fsVol, snapshotName, migration, op)
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
	revert := revert.New()
	defer revert.Fail()

	// First rename the VFS paths.
	err := genericVFSRenameVolumeSnapshot(d, vol, newSnapshotName, op)
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = genericVFSRenameVolumeSnapshot(d, newVol, vol.name, op)
	})

	// Rename the ZFS datasets.
	//_, err = subprocess.RunCommand("zfs", "rename", d.dataset(vol, false), d.dataset(newVol, false))
	out, err := d.renameSnapshot(d.dataset(vol, false), d.dataset(newVol, false))

	_ = out
	if err != nil {
		return err
	}

	revert.Add(func() {
		//_, _ = subprocess.RunCommand("zfs", "rename", d.dataset(newVol, false), d.dataset(vol, false))
		_, _ = d.renameSnapshot(d.dataset(newVol, false), d.dataset(vol, false))

	})

	// All done.
	revert.Success()

	return nil
}

// FillVolumeConfig populate volume with default config.
func (d *truenas) FillVolumeConfig(vol Volume) error {

	var excludedKeys []string

	// Copy volume.* configuration options from pool.
	// If vol has a source, ignore the block mode related config keys from the pool.
	if vol.hasSource || vol.IsVMBlock() || vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeBlock {
		excludedKeys = []string{"truenas.block_mode", "block.filesystem", "block.mount_options"}
	} else if vol.volType == VolumeTypeCustom && !vol.IsBlockBacked() {
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
		//we default block_mode to true...
		if vol.config["truenas.block_mode"] == "" {
			//vol.config["truenas.block_mode"] = "true"
		}
	}

	if vol.ContentType() == ContentTypeFS /*|| vol.IsVMBlock()*/ {
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

func (d *truenas) isBlockBacked(vol Volume) bool {
	//return util.IsTrue(vol.Config()["truenas.block_mode"])
	return vol.contentType == ContentTypeFS && vol.config["block.filesystem"] != ""
}
