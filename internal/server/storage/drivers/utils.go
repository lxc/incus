package drivers

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/operations"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

// MinBlockBoundary minimum block boundary size to use.
const MinBlockBoundary = 8192

// blockBackedAllowedFilesystems allowed filesystems for block volumes.
var blockBackedAllowedFilesystems = []string{"btrfs", "ext4", "xfs"}

// wipeDirectory empties the contents of a directory, but leaves it in place.
func wipeDirectory(path string) error {
	// List all entries.
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("Failed listing directory %q: %w", path, err)
	}

	// Individually wipe all entries.
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		err := os.RemoveAll(entryPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed removing %q: %w", entryPath, err)
		}
	}

	return nil
}

// forceRemoveAll wipes a path including any immutable/non-append files.
func forceRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		_, _ = subprocess.RunCommand("chattr", "-ai", "-R", path)
		err = os.RemoveAll(path)
		if err != nil {
			return err
		}
	}

	return nil
}

// forceUnmount unmounts stacked mounts until no mountpoint remains.
func forceUnmount(path string) (bool, error) {
	unmounted := false

	for {
		// Check if already unmounted.
		if !linux.IsMountPoint(path) {
			return unmounted, nil
		}

		// Try a clean unmount first.
		err := TryUnmount(path, 0)
		if err != nil {
			// Fallback to lazy unmounting.
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return false, fmt.Errorf("Failed to unmount '%s': %w", path, err)
			}
		}

		unmounted = true
	}
}

// mountReadOnly performs a read-only bind-mount.
func mountReadOnly(srcPath string, dstPath string) (bool, error) {
	// Check if already mounted.
	if linux.IsMountPoint(dstPath) {
		return false, nil
	}

	// Create a mount entry.
	err := TryMount(srcPath, dstPath, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	// Make it read-only.
	err = TryMount("", dstPath, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
	if err != nil {
		_, _ = forceUnmount(dstPath)
		return false, err
	}

	return true, nil
}

// sameMount checks if two paths are on the same mountpoint.
func sameMount(srcPath string, dstPath string) bool {
	// Get the source vfs path information
	var srcFsStat unix.Statfs_t
	err := unix.Statfs(srcPath, &srcFsStat)
	if err != nil {
		return false
	}

	// Get the destination vfs path information
	var dstFsStat unix.Statfs_t
	err = unix.Statfs(dstPath, &dstFsStat)
	if err != nil {
		return false
	}

	// Compare statfs
	if srcFsStat.Type != dstFsStat.Type || srcFsStat.Fsid != dstFsStat.Fsid {
		return false
	}

	// Get the source path information
	var srcStat unix.Stat_t
	err = unix.Stat(srcPath, &srcStat)
	if err != nil {
		return false
	}

	// Get the destination path information
	var dstStat unix.Stat_t
	err = unix.Stat(dstPath, &dstStat)
	if err != nil {
		return false
	}

	// Compare inode
	if srcStat.Ino != dstStat.Ino {
		return false
	}

	return true
}

// TryMount tries mounting a filesystem multiple times. This is useful for unreliable backends.
func TryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	// Attempt 20 mounts over 10s
	for i := 0; i < 20; i++ {
		err = unix.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return fmt.Errorf("Failed to mount %q on %q using %q: %w", src, dst, fs, err)
	}

	return nil
}

// TryUnmount tries unmounting a filesystem multiple times. This is useful for unreliable backends.
func TryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = unix.Unmount(path, flags)
		if err == nil {
			break
		}

		logger.Debug("Failed to unmount", logger.Ctx{"path": path, "attempt": i, "err": err})
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return fmt.Errorf("Failed to unmount %q: %w", path, err)
	}

	return nil
}

// tryExists waits up to 10s for a file to exist.
func tryExists(path string) bool {
	// Attempt 20 checks over 10s
	for i := 0; i < 20; i++ {
		if util.PathExists(path) {
			return true
		}

		time.Sleep(500 * time.Millisecond)
	}

	return false
}

// fsUUID returns the filesystem UUID for the given block path.
func fsUUID(path string) (string, error) {
	val, err := subprocess.RunCommand("blkid", "-s", "UUID", "-o", "value", path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(val), nil
}

// fsProbe returns the filesystem type for the given block path.
func fsProbe(path string) (string, error) {
	val, err := subprocess.RunCommand("blkid", "-s", "TYPE", "-o", "value", path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(val), nil
}

// GetPoolMountPath returns the mountpoint of the given pool.
// {INCUS_DIR}/storage-pools/<pool>.
func GetPoolMountPath(poolName string) string {
	return internalUtil.VarPath("storage-pools", poolName)
}

// GetVolumeMountPath returns the mount path for a specific volume based on its pool and type and
// whether it is a snapshot or not. For VolumeTypeImage the volName is the image fingerprint.
func GetVolumeMountPath(poolName string, volType VolumeType, volName string) string {
	if internalInstance.IsSnapshot(volName) {
		return internalUtil.VarPath("storage-pools", poolName, fmt.Sprintf("%s-snapshots", string(volType)), volName)
	}

	return internalUtil.VarPath("storage-pools", poolName, string(volType), volName)
}

// GetVolumeSnapshotDir gets the snapshot mount directory for the parent volume.
func GetVolumeSnapshotDir(poolName string, volType VolumeType, volName string) string {
	parent, _, _ := api.GetParentAndSnapshotName(volName)
	return internalUtil.VarPath("storage-pools", poolName, fmt.Sprintf("%s-snapshots", string(volType)), parent)
}

// GetSnapshotVolumeName returns the full volume name for a parent volume and snapshot name.
func GetSnapshotVolumeName(parentName, snapshotName string) string {
	return fmt.Sprintf("%s%s%s", parentName, internalInstance.SnapshotDelimiter, snapshotName)
}

// createParentSnapshotDirIfMissing creates the parent directory for volume snapshots.
func createParentSnapshotDirIfMissing(poolName string, volType VolumeType, volName string) error {
	snapshotsPath := GetVolumeSnapshotDir(poolName, volType, volName)

	// If it's missing, create it.
	if !util.PathExists(snapshotsPath) {
		err := os.Mkdir(snapshotsPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create parent snapshot directory %q: %w", snapshotsPath, err)
		}

		return nil
	}

	return nil
}

// deleteParentSnapshotDirIfEmpty removes the parent snapshot directory if it is empty.
// It accepts the pool name, volume type and parent volume name.
func deleteParentSnapshotDirIfEmpty(poolName string, volType VolumeType, volName string) error {
	snapshotsPath := GetVolumeSnapshotDir(poolName, volType, volName)

	// If it exists, try to delete it.
	if util.PathExists(snapshotsPath) {
		isEmpty, err := internalUtil.PathIsEmpty(snapshotsPath)
		if err != nil {
			return err
		}

		if isEmpty {
			err := os.Remove(snapshotsPath)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("Failed to remove '%s': %w", snapshotsPath, err)
			}
		}
	}

	return nil
}

// ensureSparseFile creates a sparse empty file at specified location with specified size.
// If the path already exists, the file is truncated to the requested size.
func ensureSparseFile(filePath string, sizeBytes int64) error {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("Failed to open %s: %w", filePath, err)
	}

	defer func() { _ = f.Close() }()

	err = f.Truncate(sizeBytes)
	if err != nil {
		return fmt.Errorf("Failed to create sparse file %s: %w", filePath, err)
	}

	return f.Close()
}

// ensureVolumeBlockFile creates new block file or enlarges the raw block file for a volume to the specified size.
// Returns true if resize took place, false if not. Requested size is rounded to nearest block size using
// roundVolumeBlockSizeBytes() before decision whether to resize is taken. Accepts unsupportedResizeTypes
// list that indicates which volume types it should not attempt to resize (when allowUnsafeResize=false) and
// instead return ErrNotSupported.
func ensureVolumeBlockFile(vol Volume, path string, sizeBytes int64, allowUnsafeResize bool, unsupportedResizeTypes ...VolumeType) (bool, error) {
	if sizeBytes <= 0 {
		return false, fmt.Errorf("Size cannot be zero")
	}

	// Get rounded block size to avoid QEMU boundary issues.
	sizeBytes = vol.driver.roundVolumeBlockSizeBytes(sizeBytes)

	if util.PathExists(path) {
		fi, err := os.Stat(path)
		if err != nil {
			return false, err
		}

		oldSizeBytes := fi.Size()
		if sizeBytes == oldSizeBytes {
			return false, nil
		}

		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize {
			// Reject if would try and resize a volume type that is not supported.
			// This needs to come before the ErrCannotBeShrunk check below so that any resize attempt
			// is blocked with ErrNotSupported error.
			for _, unsupportedType := range unsupportedResizeTypes {
				if unsupportedType == vol.volType {
					return false, ErrNotSupported
				}
			}

			if sizeBytes < oldSizeBytes {
				return false, fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
			}

			if vol.MountInUse() {
				return false, ErrInUse // We don't allow online resizing of block volumes.
			}
		}

		err = ensureSparseFile(path, sizeBytes)
		if err != nil {
			return false, fmt.Errorf("Failed resizing disk image %q to size %d: %w", path, sizeBytes, err)
		}

		return true, nil
	}

	// If path doesn't exist, then there has been no filler function supplied to create it from another source.
	// So instead create an empty volume (use for PXE booting a VM).
	err := ensureSparseFile(path, sizeBytes)
	if err != nil {
		return false, fmt.Errorf("Failed creating disk image %q as size %d: %w", path, sizeBytes, err)
	}

	return false, nil
}

// mkfsOptions represents options for filesystem creation.
type mkfsOptions struct {
	Label string
}

// makeFSType creates the provided filesystem.
func makeFSType(path string, fsType string, options *mkfsOptions) (string, error) {
	var err error
	var msg string

	fsOptions := options
	if fsOptions == nil {
		fsOptions = &mkfsOptions{}
	}

	cmd := []string{fmt.Sprintf("mkfs.%s", fsType)}
	if fsOptions.Label != "" {
		cmd = append(cmd, "-L", fsOptions.Label)
	}

	if fsType == "ext4" {
		cmd = append(cmd, "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0")
	}

	// Always add the path to the device as the last argument for wider compatibility with versions of mkfs.
	cmd = append(cmd, path)

	msg, err = subprocess.TryRunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return msg, err
	}

	return "", nil
}

// filesystemTypeCanBeShrunk indicates if filesystems of fsType can be shrunk.
func filesystemTypeCanBeShrunk(fsType string) bool {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	if slices.Contains([]string{"ext4", "btrfs"}, fsType) {
		return true
	}

	return false
}

// shrinkFileSystem shrinks a filesystem if it is supported.
// EXT4 volumes will be unmounted temporarily if needed.
// BTRFS volumes will be mounted temporarily if needed.
// Accepts a force argument that indicates whether to skip some safety checks when resizing the volume.
// This should only be used if the volume will be deleted on resize error.
func shrinkFileSystem(fsType string, devPath string, vol Volume, byteSize int64, force bool) error {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	if !filesystemTypeCanBeShrunk(fsType) {
		return ErrCannotBeShrunk
	}

	// The smallest unit that resize2fs accepts in byte size (rather than blocks) is kilobytes.
	strSize := fmt.Sprintf("%dK", byteSize/1024)

	switch fsType {
	case "ext4":
		return vol.UnmountTask(func(op *operations.Operation) error {
			output, err := subprocess.RunCommand("e2fsck", "-f", "-y", devPath)
			if err != nil {
				exitCodeFSModified := false
				runErr, ok := err.(subprocess.RunError)
				if ok {
					exitError, ok := runErr.Unwrap().(*exec.ExitError)
					if ok {
						if exitError.ExitCode() == 1 {
							exitCodeFSModified = true
						}
					}
				}

				// e2fsck can return non-zero exit code if it has modified the filesystem, but
				// this isn't an error and we can proceed.
				if !exitCodeFSModified {
					// e2fsck provides some context to errors on stdout.
					return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
				}
			}

			var args []string
			if force {
				// Enable force mode if requested. Should only be done if volume will be deleted
				// on error as this can result in corrupting the filesystem if fails during resize.
				// This is useful because sometimes the pre-checks performed by resize2fs are not
				// accurate and would prevent a successful filesystem shrink.
				args = append(args, "-f")
			}

			args = append(args, devPath, strSize)
			_, err = subprocess.RunCommand("resize2fs", args...)
			if err != nil {
				return err
			}

			return nil
		}, true, nil)
	case "btrfs":
		return vol.MountTask(func(mountPath string, op *operations.Operation) error {
			_, err := subprocess.RunCommand("btrfs", "filesystem", "resize", strSize, mountPath)
			if err != nil {
				return err
			}

			return nil
		}, nil)
	}

	return fmt.Errorf("Unrecognised filesystem type %q", fsType)
}

// growFileSystem grows a filesystem if it is supported. The volume will be mounted temporarily if needed.
func growFileSystem(fsType string, devPath string, vol Volume) error {
	if fsType == "" {
		fsType = DefaultFilesystem
	}

	return vol.MountTask(func(mountPath string, op *operations.Operation) error {
		var msg string
		var err error
		switch fsType {
		case "ext4":
			msg, err = subprocess.TryRunCommand("resize2fs", devPath)
		case "xfs":
			msg, err = subprocess.TryRunCommand("xfs_growfs", mountPath)
		case "btrfs":
			msg, err = subprocess.TryRunCommand("btrfs", "filesystem", "resize", "max", mountPath)
		default:
			return fmt.Errorf("Unrecognised filesystem type %q", fsType)
		}

		if err != nil {
			return fmt.Errorf("Could not grow underlying %q filesystem for %q: %s", fsType, devPath, msg)
		}

		return nil
	}, nil)
}

// renegerateFilesystemUUIDNeeded returns true if fsType requires UUID regeneration, false if not.
func renegerateFilesystemUUIDNeeded(fsType string) bool {
	switch fsType {
	case "btrfs":
		return true
	case "xfs":
		return true
	}

	return false
}

// regenerateFilesystemUUID changes the filesystem UUID to a new randomly generated one if the fsType requires it.
// Otherwise this function does nothing.
func regenerateFilesystemUUID(fsType string, devPath string) error {
	switch fsType {
	case "btrfs":
		return regenerateFilesystemBTRFSUUID(devPath)
	case "xfs":
		return regenerateFilesystemXFSUUID(devPath)
	}

	return fmt.Errorf("Filesystem not supported")
}

// regenerateFilesystemBTRFSUUID changes the BTRFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemBTRFSUUID(devPath string) error {
	// If the snapshot was taken whilst instance was running there may be outstanding transactions that will
	// cause btrfstune to corrupt superblock, so ensure these are cleared out first.
	_, err := subprocess.RunCommand("btrfs", "rescue", "zero-log", devPath)
	if err != nil {
		return err
	}

	_, err = subprocess.RunCommand("btrfstune", "-f", "-u", devPath)
	if err != nil {
		return err
	}

	return nil
}

// regenerateFilesystemXFSUUID changes the XFS filesystem UUID to a new randomly generated one.
func regenerateFilesystemXFSUUID(devPath string) error {
	// Attempt to generate a new UUID.
	msg, err := subprocess.RunCommand("xfs_admin", "-U", "generate", devPath)
	if err != nil {
		return err
	}

	if msg != "" {
		// Exit 0 with a msg usually means some log entry getting in the way.
		_, err = subprocess.RunCommand("xfs_repair", "-o", "force_geometry", "-L", devPath)
		if err != nil {
			return err
		}

		// Attempt to generate a new UUID again.
		_, err = subprocess.RunCommand("xfs_admin", "-U", "generate", devPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// copyDevice copies one device path to another using dd running at low priority.
// It expects outputPath to exist already, so will not create it.
func copyDevice(inputPath string, outputPath string) error {
	cmd := []string{
		"nice", "-n19", // Run dd with low priority to reduce CPU impact on other processes.
		"dd", fmt.Sprintf("if=%s", inputPath), fmt.Sprintf("of=%s", outputPath),
		"bs=16M",       // Use large buffer to reduce syscalls and speed up copy.
		"conv=nocreat", // Don't create output file if missing (expect caller to have created output file).
	}

	// Check for Direct I/O support.
	from, err := os.OpenFile(inputPath, unix.O_DIRECT|unix.O_RDONLY, 0)
	if err == nil {
		cmd = append(cmd, "iflag=direct")
		_ = from.Close()
	}

	to, err := os.OpenFile(outputPath, unix.O_DIRECT|unix.O_RDONLY, 0)
	if err == nil {
		cmd = append(cmd, "oflag=direct")
		_ = to.Close()
	}

	_, err = subprocess.RunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return err
	}

	return nil
}

// loopFilePath returns the loop file path for a storage pool.
func loopFilePath(poolName string) string {
	return filepath.Join(internalUtil.VarPath("disks"), fmt.Sprintf("%s.img", poolName))
}

// ShiftBtrfsRootfs shifts the BTRFS root filesystem.
func ShiftBtrfsRootfs(path string, diskIdmap *idmap.Set) error {
	return shiftBtrfsRootfs(path, diskIdmap, true)
}

// UnshiftBtrfsRootfs unshifts the BTRFS root filesystem.
func UnshiftBtrfsRootfs(path string, diskIdmap *idmap.Set) error {
	return shiftBtrfsRootfs(path, diskIdmap, false)
}

// shiftBtrfsRootfs shifts a filesystem that main include read-only subvolumes.
func shiftBtrfsRootfs(path string, diskIdmap *idmap.Set, shift bool) error {
	var err error
	roSubvols := []string{}
	subvols, _ := BTRFSSubVolumesGet(path)
	sort.Strings(subvols)
	for _, subvol := range subvols {
		subvol = filepath.Join(path, subvol)

		if !BTRFSSubVolumeIsRo(subvol) {
			continue
		}

		roSubvols = append(roSubvols, subvol)
		_ = BTRFSSubVolumeMakeRw(subvol)
	}

	if shift {
		err = diskIdmap.ShiftPath(path, nil)
	} else {
		err = diskIdmap.UnshiftPath(path, nil)
	}

	for _, subvol := range roSubvols {
		_ = BTRFSSubVolumeMakeRo(subvol)
	}

	return err
}

// BTRFSSubVolumesGet gets subvolumes.
func BTRFSSubVolumesGet(path string) ([]string, error) {
	result := []string{}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	// Unprivileged users can't get to fs internals.
	_ = filepath.WalkDir(path, func(fpath string, entry fs.DirEntry, err error) error {
		// Skip walk errors
		if err != nil {
			return nil
		}

		// Ignore the base path.
		if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
			return nil
		}

		// Subvolumes can only be directories.
		if !entry.IsDir() {
			return nil
		}

		// Check if a btrfs subvolume.
		if btrfsIsSubVolume(fpath) {
			result = append(result, strings.TrimPrefix(fpath, path))
		}

		return nil
	})

	return result, nil
}

// Deprecated: Use IsSubvolume from the Btrfs driver instead.
// btrfsIsSubvolume checks if a given path is a subvolume.
func btrfsIsSubVolume(subvolPath string) bool {
	fs := unix.Stat_t{}
	err := unix.Lstat(subvolPath, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID
	if fs.Ino != 256 {
		return false
	}

	return true
}

// BTRFSSubVolumeIsRo returns if subvolume is read only.
func BTRFSSubVolumeIsRo(path string) bool {
	output, err := subprocess.RunCommand("btrfs", "property", "get", "-ts", path)
	if err != nil {
		return false
	}

	return strings.HasPrefix(string(output), "ro=true")
}

// BTRFSSubVolumeMakeRo makes a subvolume read only. Deprecated use btrfs.setSubvolumeReadonlyProperty().
func BTRFSSubVolumeMakeRo(path string) error {
	_, err := subprocess.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "true")
	return err
}

// BTRFSSubVolumeMakeRw makes a sub volume read/write. Deprecated use btrfs.setSubvolumeReadonlyProperty().
func BTRFSSubVolumeMakeRw(path string) error {
	_, err := subprocess.RunCommand("btrfs", "property", "set", "-ts", path, "ro", "false")
	return err
}

// ShiftZFSSkipper indicates which files not to shift for ZFS.
func ShiftZFSSkipper(dir string, absPath string, fi os.FileInfo, newuid int64, newgid int64) error {
	strippedPath := absPath
	if dir != "" {
		strippedPath = absPath[len(dir):]
	}

	if fi.IsDir() && strippedPath == "/.zfs/snapshot" {
		return filepath.SkipDir
	}

	return nil
}

// BlockDiskSizeBytes returns the size of a block disk (path can be either block device or raw file).
func BlockDiskSizeBytes(blockDiskPath string) (int64, error) {
	if linux.IsBlockdevPath(blockDiskPath) {
		// Attempt to open the device path.
		f, err := os.Open(blockDiskPath)
		if err != nil {
			return -1, err
		}

		defer func() { _ = f.Close() }()
		fd := int(f.Fd())

		// Retrieve the block device size.
		res, err := unix.IoctlGetInt(fd, unix.BLKGETSIZE64)
		if err != nil {
			return -1, err
		}

		return int64(res), nil
	}

	// Block device is assumed to be a raw file.
	fi, err := os.Lstat(blockDiskPath)
	if err != nil {
		return -1, err
	}

	return fi.Size(), nil
}

// OperationLockName returns the storage specific lock name to use with locking package.
func OperationLockName(operationName string, poolName string, volType VolumeType, contentType ContentType, volName string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", operationName, poolName, volType, contentType, volName)
}

// loopFileSizeDefault returns the size in GiB to use as the default size for a pool loop file.
// This is based on the size of the filesystem of daemon's VarPath().
func loopFileSizeDefault() (uint64, error) {
	st := unix.Statfs_t{}
	err := unix.Statfs(internalUtil.VarPath(), &st)
	if err != nil {
		return 0, fmt.Errorf("Failed getting free space of %q: %w", internalUtil.VarPath(), err)
	}

	gibAvailable := uint64(st.Frsize) * st.Bavail / (1024 * 1024 * 1024)
	if gibAvailable > 30 {
		return 30, nil // Default to no more than 30GiB.
	} else if gibAvailable > 5 {
		return gibAvailable / 5, nil // Use 20% of free space otherwise.
	} else if gibAvailable == 5 {
		return gibAvailable, nil // Need at least 5GiB free.
	}

	return 0, fmt.Errorf("Insufficient free space to create default sized 5GiB pool")
}

// loopFileSetup sets up a loop device for the provided sourcePath.
// It tries to enable direct I/O if supported.
func loopDeviceSetup(sourcePath string) (string, error) {
	out, err := subprocess.RunCommand("losetup", "--find", "--nooverlap", "--direct-io=on", "--show", sourcePath)
	if err != nil {
		if strings.Contains(err.Error(), "direct io") || strings.Contains(err.Error(), "Invalid argument") {
			out, err = subprocess.RunCommand("losetup", "--find", "--nooverlap", "--show", sourcePath)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	return strings.TrimSpace(out), nil
}

// loopFileAutoDetach enables auto detach mode for a loop device.
func loopDeviceAutoDetach(loopDevPath string) error {
	_, err := subprocess.RunCommand("losetup", "--detach", loopDevPath)
	return err
}

// loopDeviceSetCapacity forces the loop driver to reread the size of the file associated with the specified loop device.
func loopDeviceSetCapacity(loopDevPath string) error {
	_, err := subprocess.RunCommand("losetup", "--set-capacity", loopDevPath)
	return err
}

// wipeBlockHeaders will wipe the first 4MB of a block device.
func wipeBlockHeaders(path string) error {
	// Open /dev/zero.
	fdZero, err := os.Open("/dev/zero")
	if err != nil {
		return err
	}

	defer fdZero.Close()

	// Open the target disk.
	fdDisk, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}

	defer fdDisk.Close()

	// Wipe the 4MiB header.
	_, err = io.CopyN(fdDisk, fdZero, 1024*1024*4)
	if err != nil {
		return err
	}

	return nil
}

// IsContentBlock returns true if the content type is either block or iso.
func IsContentBlock(contentType ContentType) bool {
	return contentType == ContentTypeBlock || contentType == ContentTypeISO
}
