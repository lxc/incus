package drivers

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	"github.com/lxc/incus/v6/internal/rsync"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

// genericVolumeBlockExtension extension used for generic block volume disk files.
const genericVolumeBlockExtension = "img"

// genericVolumeDiskFile used to indicate the file name used for block volume disk files.
const genericVolumeDiskFile = "root.img"

// genericISOVolumeSuffix suffix used for generic iso content type volumes.
const genericISOVolumeSuffix = ".iso"

// genericVFSGetResources is a generic GetResources implementation for VFS-only drivers.
func genericVFSGetResources(d Driver) (*api.ResourcesStoragePool, error) {
	// Get the VFS information
	st, err := linux.StatVFS(GetPoolMountPath(d.Name()))
	if err != nil {
		return nil, err
	}

	// Fill in the struct
	res := api.ResourcesStoragePool{}
	res.Space.Total = st.Blocks * uint64(st.Bsize)
	res.Space.Used = (st.Blocks - st.Bfree) * uint64(st.Bsize)

	// Some filesystems don't report inodes since they allocate them
	// dynamically e.g. btrfs.
	if st.Files > 0 {
		res.Inodes.Total = st.Files
		res.Inodes.Used = st.Files - st.Ffree
	}

	return &res, nil
}

// genericVFSRenameVolume is a generic RenameVolume implementation for VFS-only drivers.
func genericVFSRenameVolume(d Driver, vol Volume, newVolName string, op *operations.Operation) error {
	if vol.IsSnapshot() {
		return errors.New("Volume must not be a snapshot")
	}

	reverter := revert.New()
	defer reverter.Fail()

	volName := vol.name

	// Add a .iso suffix to ISO volumes.
	if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeISO {
		volName = volName + genericISOVolumeSuffix
		newVolName = newVolName + genericISOVolumeSuffix
	}

	// Rename the volume itself.
	srcVolumePath := GetVolumeMountPath(d.Name(), vol.volType, volName)
	dstVolumePath := GetVolumeMountPath(d.Name(), vol.volType, newVolName)

	if util.PathExists(srcVolumePath) {
		err := os.Rename(srcVolumePath, dstVolumePath)
		if err != nil {
			return fmt.Errorf("Failed to rename %q to %q: %w", srcVolumePath, dstVolumePath, err)
		}

		reverter.Add(func() { _ = os.Rename(dstVolumePath, srcVolumePath) })
	}

	// And if present, the snapshots too.
	srcSnapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, vol.name)
	dstSnapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, newVolName)

	if util.PathExists(srcSnapshotDir) {
		err := os.Rename(srcSnapshotDir, dstSnapshotDir)
		if err != nil {
			return fmt.Errorf("Failed to rename %q to %q: %w", srcSnapshotDir, dstSnapshotDir, err)
		}

		reverter.Add(func() { _ = os.Rename(dstSnapshotDir, srcSnapshotDir) })
	}

	reverter.Success()
	return nil
}

// genericVFSVolumeSnapshots is a generic VolumeSnapshots implementation for VFS-only drivers.
func genericVFSVolumeSnapshots(d Driver, vol Volume, op *operations.Operation) ([]string, error) {
	snapshotDir := GetVolumeSnapshotDir(d.Name(), vol.volType, vol.name)
	snapshots := []string{}

	ents, err := os.ReadDir(snapshotDir)
	if err != nil {
		// If the snapshots directory doesn't exist, there are no snapshots.
		if errors.Is(err, fs.ErrNotExist) {
			return snapshots, nil
		}

		return nil, fmt.Errorf("Failed to list directory %q: %w", snapshotDir, err)
	}

	for _, ent := range ents {
		fileInfo, err := os.Stat(filepath.Join(snapshotDir, ent.Name()))
		if err != nil {
			return nil, err
		}

		if !fileInfo.IsDir() {
			continue
		}

		snapshots = append(snapshots, ent.Name())
	}

	return snapshots, nil
}

// genericVFSRenameVolumeSnapshot is a generic RenameVolumeSnapshot implementation for VFS-only drivers.
func genericVFSRenameVolumeSnapshot(d Driver, snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	if !snapVol.IsSnapshot() {
		return errors.New("Volume must be a snapshot")
	}

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	oldPath := snapVol.MountPath()
	newPath := GetVolumeMountPath(d.Name(), snapVol.volType, GetSnapshotVolumeName(parentName, newSnapshotName))

	if util.PathExists(oldPath) {
		err := os.Rename(oldPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed to rename %q to %q: %w", oldPath, newPath, err)
		}
	}

	return nil
}

// genericVFSMigrateVolume is a generic MigrateVolume implementation for VFS-only drivers.
func genericVFSMigrateVolume(d Driver, s *state.State, vol Volume, conn io.ReadWriteCloser, volSrcArgs *localMigration.VolumeSourceArgs, op *operations.Operation) error {
	bwlimit := d.Config()["rsync.bwlimit"]
	var rsyncArgs []string

	// For VM volumes, exclude the generic root disk image file from being transferred via rsync, as it will
	// be transferred later using a different method.
	if vol.IsVMBlock() {
		if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC {
			return ErrNotSupported
		}

		rsyncArgs = []string{"--exclude", genericVolumeDiskFile}
	} else if vol.contentType == ContentTypeBlock && volSrcArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC || vol.contentType == ContentTypeFS && volSrcArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

	// Define function to send a filesystem volume.
	sendFSVol := func(vol Volume, conn io.ReadWriteCloser, mountPath string) error {
		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = localMigration.ProgressTracker(op, "fs_progress", vol.name)
		}

		path := internalUtil.AddSlash(mountPath)

		d.Logger().Debug("Sending filesystem volume", logger.Ctx{"volName": vol.name, "path": path, "bwlimit": bwlimit, "rsyncArgs": rsyncArgs})
		err := rsync.Send(vol.name, path, conn, wrapper, volSrcArgs.MigrationType.Features, bwlimit, s.OS.ExecPath, rsyncArgs...)

		status, _ := linux.ExitStatus(err)
		if volSrcArgs.AllowInconsistent && status == 24 {
			return nil
		}

		return err
	}

	// Define function to send a block volume.
	sendBlockVol := func(vol Volume, conn io.ReadWriteCloser) error {
		// Close when done to indicate to target side we are finished sending this volume.
		defer func() { _ = conn.Close() }()

		var wrapper *ioprogress.ProgressTracker
		if volSrcArgs.TrackProgress {
			wrapper = localMigration.ProgressTracker(op, "block_progress", vol.name)
		}

		path, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return fmt.Errorf("Error getting VM block volume disk path: %w", err)
		}

		from, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Error opening file for reading %q: %w", path, err)
		}

		defer func() { _ = from.Close() }()

		// Setup progress tracker.
		fromPipe := io.ReadCloser(from)
		if wrapper != nil {
			fromPipe = &ioprogress.ProgressReader{
				ReadCloser: fromPipe,
				Tracker:    wrapper,
			}
		}

		d.Logger().Debug("Sending block volume", logger.Ctx{"volName": vol.name, "path": path})
		_, err = util.SafeCopy(conn, fromPipe)
		if err != nil {
			return fmt.Errorf("Error copying %q to migration connection: %w", path, err)
		}

		err = from.Close()
		if err != nil {
			return fmt.Errorf("Failed to close file %q: %w", path, err)
		}

		return nil
	}

	// Send all snapshots to target.
	for _, snapName := range volSrcArgs.Snapshots {
		snapshot, err := vol.NewSnapshot(snapName)
		if err != nil {
			return err
		}

		// Send snapshot to target (ensure local snapshot volume is mounted if needed).
		err = snapshot.MountTask(func(mountPath string, op *operations.Operation) error {
			if vol.contentType != ContentTypeBlock || vol.volType != VolumeTypeCustom {
				err := sendFSVol(snapshot, conn, mountPath)
				if err != nil {
					return err
				}
			}

			if vol.IsVMBlock() || (vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom) {
				err = sendBlockVol(snapshot, conn)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}
	}

	// Send volume to target (ensure local volume is mounted if needed).
	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		if !IsContentBlock(vol.contentType) || vol.volType != VolumeTypeCustom {
			err := sendFSVol(vol, conn, mountPath)
			if err != nil {
				return err
			}
		}

		if vol.IsVMBlock() || (IsContentBlock(vol.contentType) && vol.volType == VolumeTypeCustom) {
			err := sendBlockVol(vol, conn)
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
}

// genericVFSCreateVolumeFromMigration receives a volume and its snapshots over a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericVFSCreateVolumeFromMigration(d Driver, initVolume func(vol Volume) (revert.Hook, error), vol Volume, conn io.ReadWriteCloser, volTargetArgs localMigration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	// Check migration transport type matches volume type.
	if IsContentBlock(vol.contentType) {
		if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_BLOCK_AND_RSYNC {
			return ErrNotSupported
		}
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_RSYNC {
		return ErrNotSupported
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Create the main volume if not refreshing.
	if !volTargetArgs.Refresh {
		err := d.CreateVolume(vol, preFiller, op)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = d.DeleteVolume(vol, op) })
	}

	recvFSVol := func(volName string, conn io.ReadWriteCloser, path string) error {
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = localMigration.ProgressTracker(op, "fs_progress", volName)
		}

		d.Logger().Debug("Receiving filesystem volume started", logger.Ctx{"volName": volName, "path": path, "features": volTargetArgs.MigrationType.Features})
		defer d.Logger().Debug("Receiving filesystem volume stopped", logger.Ctx{"volName": volName, "path": path})

		return rsync.Recv(path, conn, wrapper, volTargetArgs.MigrationType.Features)
	}

	recvBlockVol := func(volName string, conn io.ReadWriteCloser, path string) error {
		var wrapper *ioprogress.ProgressTracker
		if volTargetArgs.TrackProgress {
			wrapper = localMigration.ProgressTracker(op, "block_progress", volName)
		}

		// Reset the disk.
		err := linux.ClearBlock(path, 0)
		if err != nil {
			return err
		}

		to, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return fmt.Errorf("Error opening file for writing %q: %w", path, err)
		}

		defer func() { _ = to.Close() }()

		// Setup progress tracker.
		fromPipe := io.ReadCloser(conn)
		if wrapper != nil {
			fromPipe = &ioprogress.ProgressReader{
				ReadCloser: fromPipe,
				Tracker:    wrapper,
			}
		}

		d.Logger().Debug("Receiving block volume started", logger.Ctx{"volName": volName, "path": path})
		defer d.Logger().Debug("Receiving block volume stopped", logger.Ctx{"volName": volName, "path": path})

		toPipe := io.Writer(to)
		if !d.Info().ZeroUnpack {
			toPipe = NewSparseFileWrapper(to)
		}

		_, err = util.SafeCopy(toPipe, fromPipe)
		if err != nil {
			return fmt.Errorf("Error copying from migration connection to %q: %w", path, err)
		}

		return to.Close()
	}

	// Ensure the volume is mounted.
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var err error

		// Setup paths to the main volume. We will receive each snapshot to these paths and then create
		// a snapshot of the main volume for each one.
		path := internalUtil.AddSlash(mountPath)
		pathBlock := ""

		if vol.IsVMBlock() || (IsContentBlock(vol.contentType) && vol.volType == VolumeTypeCustom) {
			pathBlock, err = d.GetVolumeDiskPath(vol)
			if err != nil {
				return fmt.Errorf("Error getting VM block volume disk path: %w", err)
			}
		}

		// Snapshots are sent first by the sender, so create these first.
		for _, snapshot := range volTargetArgs.Snapshots {
			fullSnapshotName := GetSnapshotVolumeName(vol.name, snapshot.GetName())

			snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapshotName, vol.config, vol.poolConfig)

			if snapVol.contentType != ContentTypeBlock || snapVol.volType != VolumeTypeCustom { // Receive the filesystem snapshot first (as it is sent first).
				err = recvFSVol(snapVol.name, conn, path)
				if err != nil {
					return err
				}
			}

			// Receive the block snapshot next (if needed).
			if vol.IsVMBlock() || (vol.contentType == ContentTypeBlock && vol.volType == VolumeTypeCustom) {
				err = recvBlockVol(snapVol.name, conn, pathBlock)
				if err != nil {
					return err
				}

				volSize, err := units.ParseByteSizeString(migration.GetSnapshotConfigValue(snapshot, "size"))
				if err != nil {
					return err
				}

				// During migration (e.g., LVM → dir), the block file may be smaller because
				// recvBlockVol uses SparseFileWrapper, which omits trailing zero bytes and does not truncate.
				// enlargeVolumeBlockFile ensures the block file matches the source volume size by applying truncation.
				if volSize > 0 {
					err = enlargeVolumeBlockFile(pathBlock, volSize)
					if err != nil {
						return err
					}
				}
			}

			// Create the snapshot itself.
			d.Logger().Debug("Creating snapshot", logger.Ctx{"volName": snapVol.Name()})
			err = d.CreateVolumeSnapshot(snapVol, op)
			if err != nil {
				return err
			}

			// Setup the revert.
			reverter.Add(func() {
				_ = d.DeleteVolumeSnapshot(snapVol, op)
			})
		}

		// Run volume-specific init logic.
		if initVolume != nil {
			_, err := initVolume(vol)
			if err != nil {
				return err
			}
		}

		if !IsContentBlock(vol.contentType) || vol.volType != VolumeTypeCustom {
			// Receive main volume.
			err = recvFSVol(vol.name, conn, path)
			if err != nil {
				return err
			}
		}

		// Receive the final main volume sync if needed.
		if volTargetArgs.Live && (!IsContentBlock(vol.contentType) || (vol.volType != VolumeTypeCustom && vol.volType != VolumeTypeVM)) {
			d.Logger().Debug("Starting main volume final sync", logger.Ctx{"volName": vol.name, "path": path})
			err = recvFSVol(vol.name, conn, path)
			if err != nil {
				return err
			}
		}

		// Run EnsureMountPath after mounting and syncing to ensure the mounted directory has the
		// correct permissions set.
		err = vol.EnsureMountPath(false)
		if err != nil {
			return err
		}

		// Receive the block volume next (if needed).
		if vol.IsVMBlock() || (IsContentBlock(vol.contentType) && vol.volType == VolumeTypeCustom) {
			err = recvBlockVol(vol.name, conn, pathBlock)
			if err != nil {
				return err
			}

			// During migration (e.g., LVM → dir), the block file may be smaller because
			// recvBlockVol uses SparseFileWrapper, which omits trailing zero bytes and does not truncate.
			// enlargeVolumeBlockFile ensures the block file matches the source volume size by applying truncation.
			if volTargetArgs.VolumeSize > 0 {
				err = enlargeVolumeBlockFile(pathBlock, volTargetArgs.VolumeSize)
				if err != nil {
					return err
				}
			}
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	reverter.Success()
	return nil
}

// genericVFSHasVolume is a generic HasVolume implementation for VFS-only drivers.
func genericVFSHasVolume(vol Volume) (bool, error) {
	_, err := os.Lstat(vol.MountPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// genericVFSGetVolumeDiskPath is a generic GetVolumeDiskPath implementation for VFS-only drivers.
func genericVFSGetVolumeDiskPath(vol Volume) (string, error) {
	if !IsContentBlock(vol.contentType) {
		return "", ErrNotSupported
	}

	return filepath.Join(vol.MountPath(), genericVolumeDiskFile), nil
}

// genericVFSBackupVolume is a generic BackupVolume implementation for VFS-only drivers.
func genericVFSBackupVolume(d Driver, vol Volume, writer instancewriter.InstanceWriter, basePrefix string, snapshots []string, op *operations.Operation) error {
	if len(snapshots) > 0 {
		// Check requested snapshot match those in storage.
		err := vol.SnapshotsMatch(snapshots, op)
		if err != nil {
			return err
		}
	}

	// Handle snapshots.
	if len(snapshots) > 0 {
		for _, snapName := range snapshots {
			prefix := filepath.Join(basePrefix, BackupSnapshotPrefix(vol), snapName)
			snapVol, err := vol.NewSnapshot(snapName)
			if err != nil {
				return err
			}

			err = snapVol.MountTask(func(mountPath string, op *operations.Operation) error {
				err := BackupVolume(d, snapVol, writer, mountPath, prefix)
				if err != nil {
					return err
				}

				return nil
			}, op)
			if err != nil {
				return err
			}
		}
	}

	// Copy the main volume itself.
	err := vol.MountTask(func(mountPath string, op *operations.Operation) error {
		err := BackupVolume(d, vol, writer, mountPath, filepath.Join(basePrefix, BackupPrefix(vol)))
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	return nil
}

// genericVFSBackupUnpack unpacks a non-optimized backup tarball through a storage driver.
// Returns a post hook function that should be called once the database entries for the restored backup have been
// created and a revert function that can be used to undo the actions this function performs should something
// subsequently fail. For VolumeTypeCustom volumes, a nil post hook is returned as it is expected that the DB
// record be created before the volume is unpacked due to differences in the archive format that allows this.
func genericVFSBackupUnpack(d Driver, sysOS *sys.OS, vol Volume, snapshots []string, srcData io.ReadSeeker, basePrefix string, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	reverter := revert.New()
	defer reverter.Fail()

	// Find the compression algorithm used for backup source data.
	_, err := srcData.Seek(0, io.SeekStart)
	if err != nil {
		return nil, nil, err
	}

	tarArgs, _, unpacker, err := archive.DetectCompressionFile(srcData)
	if err != nil {
		return nil, nil, err
	}

	volExists, err := d.HasVolume(vol)
	if err != nil {
		return nil, nil, err
	}

	if volExists {
		return nil, nil, errors.New("Cannot restore volume, already exists on target")
	}

	// Create new empty volume.
	err = d.CreateVolume(vol, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	reverter.Add(func() { _ = d.DeleteVolume(vol, op) })

	if len(snapshots) > 0 {
		// Create new snapshots directory.
		err := createParentSnapshotDirIfMissing(d.Name(), vol.volType, vol.name)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, snapName := range snapshots {
		err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
			backupSnapshotPrefix := filepath.Join(basePrefix, BackupSnapshotPrefix(vol), snapName)
			return UnpackVolume(d, vol, srcData, tarArgs, unpacker, backupSnapshotPrefix, mountPath)
		}, op)
		if err != nil {
			return nil, nil, err
		}

		snapVol, err := vol.NewSnapshot(snapName)
		if err != nil {
			return nil, nil, err
		}

		d.Logger().Debug("Creating volume snapshot", logger.Ctx{"snapshotName": snapVol.Name()})
		err = d.CreateVolumeSnapshot(snapVol, op)
		if err != nil {
			return nil, nil, err
		}

		if vol.IsVMBlock() && vol.Config()["block.type"] == BlockVolumeTypeQcow2 {
			fsParentVol := vol.NewVMBlockFilesystemVolume()
			fsVol := snapVol.NewVMBlockFilesystemVolume()
			err := Qcow2CreateConfigSnapshot(fsParentVol, fsVol, op)
			if err != nil {
				return nil, nil, err
			}
		}

		reverter.Add(func() { _ = d.DeleteVolumeSnapshot(snapVol, op) })
	}

	err = d.MountVolume(vol, op)
	if err != nil {
		return nil, nil, err
	}

	reverter.Add(func() { _, _ = d.UnmountVolume(vol, false, op) })

	mountPath := vol.MountPath()
	err = UnpackVolume(d, vol, srcData, tarArgs, unpacker, filepath.Join(basePrefix, BackupPrefix(vol)), mountPath)
	if err != nil {
		return nil, nil, err
	}

	// Run EnsureMountPath after mounting and unpacking to ensure the mounted directory has the
	// correct permissions set.
	err = vol.EnsureMountPath(false)
	if err != nil {
		return nil, nil, err
	}

	cleanup := reverter.Clone().Fail // Clone before calling reverter.Success() so we can return the Fail func.
	reverter.Success()

	var postHook VolumePostHook
	if vol.volType != VolumeTypeCustom {
		// Leave volume mounted (as is needed during backup.yaml generation during latter parts of the
		// backup restoration process). Create a post hook function that will be called at the end of the
		// backup restore process to unmount the volume if needed.
		postHook = func(vol Volume) error {
			_, err = d.UnmountVolume(vol, false, op)
			if err != nil {
				return err
			}

			return nil
		}
	} else {
		// For custom volumes unmount now, there is no post hook as there is no backup.yaml to generate.
		_, err = d.UnmountVolume(vol, false, op)
		if err != nil {
			return nil, nil, err
		}
	}

	return postHook, cleanup, nil
}

// genericVFSCopyVolume copies a volume and its snapshots using a non-optimized method.
// initVolume is run against the main volume (not the snapshots) and is often used for quota initialization.
func genericVFSCopyVolume(d Driver, initVolume func(vol Volume) (revert.Hook, error), vol Volume, srcVol Volume, srcSnapshots []Volume, refresh bool, allowInconsistent bool, op *operations.Operation) error {
	if vol.contentType != srcVol.contentType {
		return errors.New("Content type of source and target must be the same")
	}

	bwlimit := d.Config()["rsync.bwlimit"]

	var rsyncArgs []string

	if srcVol.IsVMBlock() {
		rsyncArgs = append(rsyncArgs, "--exclude", genericVolumeDiskFile)
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Create the main volume if not refreshing.
	if !refresh {
		err := d.CreateVolume(vol, nil, op)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = d.DeleteVolume(vol, op) })
	}

	// Define function to send a filesystem volume.
	sendFSVol := func(srcPath string, targetPath string) error {
		d.Logger().Debug("Copying filesystem volume", logger.Ctx{"sourcePath": srcPath, "targetPath": targetPath, "bwlimit": bwlimit, "rsyncArgs": rsyncArgs})
		_, err := rsync.LocalCopy(srcPath, targetPath, bwlimit, true, rsyncArgs...)

		status, _ := linux.ExitStatus(err)
		if allowInconsistent && status == 24 {
			return nil
		}

		return err
	}

	// Define function to send a block volume.
	sendBlockVol := func(srcVol Volume, targetVol Volume) error {
		srcDevPath, err := d.GetVolumeDiskPath(srcVol)
		if err != nil {
			return err
		}

		targetDevPath, err := d.GetVolumeDiskPath(targetVol)
		if err != nil {
			return err
		}

		d.Logger().Debug("Copying block volume", logger.Ctx{"srcDevPath": srcDevPath, "targetPath": targetDevPath})
		err = copyDevice(srcDevPath, targetDevPath)
		if err != nil {
			return err
		}

		return nil
	}

	// Ensure the volume is mounted.
	err := vol.MountTask(func(targetMountPath string, op *operations.Operation) error {
		// If copying snapshots is indicated, check the source isn't itself a snapshot.
		if len(srcSnapshots) > 0 && !srcVol.IsSnapshot() {
			for _, srcVol := range srcSnapshots {
				_, snapName, _ := api.GetParentAndSnapshotName(srcVol.name)

				// Mount the source snapshot and copy it to the target main volume.
				// A snapshot will then be taken next so it is stored in the correct volume and
				// subsequent filesystem rsync transfers benefit from only transferring the files
				// that changed between snapshots.
				err := srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
					if srcVol.contentType != ContentTypeBlock || srcVol.volType != VolumeTypeCustom {
						err := sendFSVol(srcMountPath, targetMountPath)
						if err != nil {
							return err
						}
					}

					if srcVol.IsVMBlock() || srcVol.contentType == ContentTypeBlock && srcVol.volType == VolumeTypeCustom {
						err := sendBlockVol(srcVol, vol)
						if err != nil {
							return err
						}
					}

					return nil
				}, op)
				if err != nil {
					return err
				}

				fullSnapName := GetSnapshotVolumeName(vol.name, snapName)
				snapVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, fullSnapName, vol.config, vol.poolConfig)

				// Create the snapshot itself.
				d.Logger().Debug("Creating snapshot", logger.Ctx{"volName": snapVol.Name()})
				err = d.CreateVolumeSnapshot(snapVol, op)
				if err != nil {
					return err
				}

				// Setup the revert.
				reverter.Add(func() {
					_ = d.DeleteVolumeSnapshot(snapVol, op)
				})
			}
		}

		// Run volume-specific init logic.
		if initVolume != nil {
			_, err := initVolume(vol)
			if err != nil {
				return err
			}
		}

		// Copy source to destination (mounting each volume if needed).
		err := srcVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			if srcVol.contentType != ContentTypeBlock || srcVol.volType != VolumeTypeCustom {
				err := sendFSVol(srcMountPath, targetMountPath)
				if err != nil {
					return err
				}
			}

			if srcVol.IsVMBlock() || srcVol.contentType == ContentTypeBlock && srcVol.volType == VolumeTypeCustom {
				err := sendBlockVol(srcVol, vol)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}

		// Run EnsureMountPath after mounting and copying to ensure the mounted directory has the
		// correct permissions set.
		err = vol.EnsureMountPath(false)
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	reverter.Success()
	return nil
}

// genericVFSListVolumes returns a list of volumes in storage pool.
func genericVFSListVolumes(d Driver) ([]Volume, error) {
	var vols []Volume
	poolName := d.Name()
	poolConfig := d.Config()
	poolMountPath := GetPoolMountPath(poolName)

	for _, volType := range d.Info().VolumeTypes {
		if len(BaseDirectories[volType].Paths) < 1 {
			return nil, fmt.Errorf("Cannot get base directory name for volume type %q", volType)
		}

		volTypePath := filepath.Join(poolMountPath, BaseDirectories[volType].Paths[0])
		ents, err := os.ReadDir(volTypePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to list directory %q for volume type %q: %w", volTypePath, volType, err)
		}

		for _, ent := range ents {
			volName := ent.Name()

			contentType := ContentTypeFS
			if volType == VolumeTypeVM {
				contentType = ContentTypeBlock
			} else if volType == VolumeTypeCustom && util.PathExists(filepath.Join(volTypePath, volName, genericVolumeDiskFile)) {
				if strings.HasSuffix(ent.Name(), genericISOVolumeSuffix) {
					contentType = ContentTypeISO
					volName = strings.TrimSuffix(volName, genericISOVolumeSuffix)
				} else {
					contentType = ContentTypeBlock
				}
			}

			vols = append(vols, NewVolume(d, poolName, volType, contentType, volName, make(map[string]string), poolConfig))
		}
	}

	return vols, nil
}

// genericRunFiller runs the supplied filler, and setting the returned volume size back into filler.
func genericRunFiller(d Driver, vol Volume, devPath string, filler *VolumeFiller, allowUnsafeResize bool) error {
	if filler == nil || filler.Fill == nil {
		return nil
	}

	vol.driver.Logger().Debug("Running filler function", logger.Ctx{"dev": devPath, "path": vol.MountPath()})
	volSize, err := filler.Fill(vol, devPath, allowUnsafeResize, !d.Info().ZeroUnpack, d.Info().TargetFormat)
	if err != nil {
		return err
	}

	filler.Size = volSize

	return nil
}
