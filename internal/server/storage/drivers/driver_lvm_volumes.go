package drivers

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/rsync"
	"github.com/lxc/incus/v6/internal/server/backup"
	"github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied filler function.
func (d *lvm) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	volPath := vol.MountPath()
	err := vol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.RemoveAll(volPath) })

	err = d.createLogicalVolume(d.config["lvm.vg_name"], d.thinpoolName(), vol, d.usesThinpool())
	if err != nil {
		return fmt.Errorf("Error creating LVM logical volume: %w", err)
	}

	revert.Add(func() { _ = d.DeleteVolume(vol, op) })

	// For VMs, also create the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		err := d.CreateVolume(fsVol, nil, op)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.DeleteVolume(fsVol, op) })
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
			if vol.volType == VolumeTypeImage || !d.usesThinpool() {
				// Allow filler to resize initial image and non-thin volumes as needed.
				// Some storage drivers don't normally allow image volumes to be resized due to
				// them having read-only snapshots that cannot be resized. However when creating
				// the initial volume and filling it unsafe resizing can be allowed and is required
				// in order to support unpacking images larger than the default volume size.
				// The filler function is still expected to obey any volume size restrictions
				// configured on the pool.
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
			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// CreateVolumeFromBackup restores a backup tarball onto the storage device.
func (d *lvm) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *lvm) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	var err error
	var srcSnapshots []Volume

	if copySnapshots && !srcVol.IsSnapshot() {
		// Get the list of snapshots from the source.
		srcSnapshots, err = srcVol.Snapshots(op)
		if err != nil {
			return err
		}
	}

	// We can use optimised copying when the pool is backed by an LVM thinpool.
	if d.usesThinpool() {
		err = d.copyThinpoolVolume(vol, srcVol, srcSnapshots, false)
		if err != nil {
			return err
		}

		// For VMs, also copy the filesystem volume.
		if vol.IsVMBlock() {
			srcFSVol := srcVol.NewVMBlockFilesystemVolume()
			fsVol := vol.NewVMBlockFilesystemVolume()
			return d.copyThinpoolVolume(fsVol, srcFSVol, srcSnapshots, false)
		}

		return nil
	}

	// Otherwise run the generic copy.
	return genericVFSCopyVolume(d, nil, vol, srcVol, srcSnapshots, false, allowInconsistent, op)
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *lvm) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if d.clustered && volTargetArgs.ClusterMoveSourceName != "" {
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		// Mark the volume for shared locking during live migration.
		if vol.volType == VolumeTypeVM || vol.IsCustomBlock() {
			volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.Name())
			_, err := subprocess.RunCommand("lvchange", "--activate", "sy", "--ignoreactivationskip", volDevPath)
			if err != nil {
				return err
			}

			go func(volDevPath string) {
				// Attempt to re-lock as host-exclusive as soon as possible.
				// This will only happen once the migration on the source system is fully over.
				// Re-try every 10s until success as we can't predict how long a migration may take (from a few seconds to hours).
				for {
					_, err := subprocess.RunCommand("lvchange", "--activate", "ey", "--ignoreactivationskip", volDevPath)
					if err == nil {
						break
					}

					time.Sleep(10 * time.Second)
				}
			}(volDevPath)

			if vol.IsVMBlock() {
				fsVol := vol.NewVMBlockFilesystemVolume()
				err := d.CreateVolumeFromMigration(fsVol, conn, volTargetArgs, preFiller, op)
				if err != nil {
					return err
				}
			}
		}

		return nil
	}

	return genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
}

// RefreshVolume provides same-pool volume and specific snapshots syncing functionality.
func (d *lvm) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, allowInconsistent bool, op *operations.Operation) error {
	// We can use optimised copying when the pool is backed by an LVM thinpool.
	if d.usesThinpool() {
		return d.copyThinpoolVolume(vol, srcVol, srcSnapshots, true)
	}

	// Otherwise run the generic copy.
	return genericVFSCopyVolume(d, nil, vol, srcVol, srcSnapshots, true, allowInconsistent, op)
}

// DeleteVolume deletes a volume of the storage device. If any snapshots of the volume remain then this function
// will return an error.
func (d *lvm) DeleteVolume(vol Volume, op *operations.Operation) error {
	snapshots, err := d.VolumeSnapshots(vol, op)
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return fmt.Errorf("Cannot remove a volume that has snapshots")
	}

	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
	lvExists, err := d.logicalVolumeExists(volDevPath)
	if err != nil {
		return err
	}

	if lvExists {
		if vol.contentType == ContentTypeFS {
			_, err = d.UnmountVolume(vol, false, op)
			if err != nil {
				return fmt.Errorf("Error unmounting LVM logical volume: %w", err)
			}
		}

		err = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name))
		if err != nil {
			return fmt.Errorf("Error removing LVM logical volume: %w", err)
		}
	}

	if vol.contentType == ContentTypeFS {
		// Remove the volume from the storage device.
		mountPath := vol.MountPath()
		err = os.RemoveAll(mountPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error removing LVM logical volume mount path %q: %w", mountPath, err)
		}

		// Although the volume snapshot directory should already be removed, lets remove it here to just in
		// case the top-level directory is left.
		err = deleteParentSnapshotDirIfEmpty(d.name, vol.volType, vol.name)
		if err != nil {
			return err
		}
	}

	// For VMs, also delete the filesystem volume.
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
func (d *lvm) HasVolume(vol Volume) (bool, error) {
	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
	return d.logicalVolumeExists(volDevPath)
}

// FillVolumeConfig populate volume with default config.
func (d *lvm) FillVolumeConfig(vol Volume) error {
	// Copy volume.* configuration options from pool.
	// Exclude "block.filesystem" and "block.mount_options" as they depend on volume type (handled below).
	// Exclude "lvm.stripes", "lvm.stripes.size" as they only work on non-thin storage pools (handled below).
	err := d.fillVolumeConfig(&vol, "block.filesystem", "block.mount_options", "lvm.stripes", "lvm.stripes.size")
	if err != nil {
		return err
	}

	// Only validate filesystem config keys for filesystem volumes or VM block volumes (which have an
	// associated filesystem volume).
	if vol.ContentType() == ContentTypeFS || vol.IsVMBlock() {
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

	// Inherit stripe settings from pool if not set and not using thin pool.
	if !d.usesThinpool() {
		if vol.config["lvm.stripes"] == "" {
			vol.config["lvm.stripes"] = d.config["volume.lvm.stripes"]
		}

		if vol.config["lvm.stripes.size"] == "" {
			vol.config["lvm.stripes.size"] = d.config["lvm.stripes.size"]
		}
	}

	return nil
}

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *lvm) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		"block.mount_options": validate.IsAny,
		"block.filesystem":    validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		"lvm.stripes":         validate.Optional(validate.IsUint32),
		"lvm.stripes.size":    validate.Optional(validate.IsSize),
	}
}

// ValidateVolume validates the supplied volume config.
func (d *lvm) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	err := d.validateVolume(vol, d.commonVolumeRules(), removeUnknownKeys)
	if err != nil {
		return err
	}

	if d.usesThinpool() && vol.config["lvm.stripes"] != "" {
		return fmt.Errorf("lvm.stripes cannot be used with thin pool volumes")
	}

	if d.usesThinpool() && vol.config["lvm.stripes.size"] != "" {
		return fmt.Errorf("lvm.stripes.size cannot be used with thin pool volumes")
	}

	return nil
}

// UpdateVolume applies config changes to the volume.
func (d *lvm) UpdateVolume(vol Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.SetVolumeQuota(vol, newSize, false, nil)
		if err != nil {
			return err
		}
	}

	_, changed := changedConfig["lvm.stripes"]
	if changed {
		return fmt.Errorf("lvm.stripes cannot be changed")
	}

	_, changed = changedConfig["lvm.stripes.size"]
	if changed {
		return fmt.Errorf("lvm.stripes.size cannot be changed")
	}

	return nil
}

// GetVolumeUsage returns the disk space used by the volume (this is not currently supported).
func (d *lvm) GetVolumeUsage(vol Volume) (int64, error) {
	// Snapshot usage not supported for LVM.
	if vol.IsSnapshot() {
		return -1, ErrNotSupported
	}

	// For non-snapshot filesystem volumes, we only return usage when the volume is mounted.
	// This is because to get an accurate value we cannot use blocks allocated, as the filesystem will likely
	// consume blocks and not free them when files are deleted in the volume. This avoids returning different
	// values depending on whether the volume is mounted or not.
	if vol.contentType == ContentTypeFS && linux.IsMountPoint(vol.MountPath()) {
		var stat unix.Statfs_t
		err := unix.Statfs(vol.MountPath(), &stat)
		if err != nil {
			return -1, err
		}

		return int64(stat.Blocks-stat.Bfree) * int64(stat.Bsize), nil
	} else if vol.contentType == ContentTypeBlock && d.usesThinpool() {
		// For non-snapshot thin pool block volumes we can calculate an approximate usage using the space
		// allocated to the volume from the thin pool.
		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
		_, usedSize, err := d.thinPoolVolumeUsage(volDevPath)
		if err != nil {
			return -1, err
		}

		return int64(usedSize), nil
	}

	return -1, ErrNotSupported
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size.
func (d *lvm) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	// Do nothing if size isn't specified.
	if size == "" || size == "0" {
		return nil
	}

	sizeBytes, err := d.roundedSizeBytesString(size)
	if err != nil {
		return err
	}

	// Read actual size of current volume.
	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
	oldSizeBytes, err := d.logicalVolumeSize(volDevPath)
	if err != nil {
		return err
	}

	// Get the volume group's physical extent size, as we use this to figure out if the new and old sizes are
	// going to change beyond 1 extent size, otherwise there is no point in trying to resize as LVM do it.
	vgExtentSize, err := d.volumeGroupExtentSize(d.config["lvm.vg_name"])
	if err != nil {
		return err
	}

	// Round up the number of extents required for new quota size, as this is what the lvresize tool will do.
	newNumExtents := math.Ceil(float64(sizeBytes) / float64(vgExtentSize))
	oldNumExtents := math.Ceil(float64(oldSizeBytes) / float64(vgExtentSize))
	extentDiff := int(newNumExtents - oldNumExtents)

	// If old and new extents required are the same, nothing to do, as LVM won't resize them.
	if extentDiff == 0 {
		return nil
	}

	l := d.logger.AddContext(logger.Ctx{"dev": volDevPath, "size": fmt.Sprintf("%db", sizeBytes)})

	// Activate volume if needed.
	activated, err := d.activateVolume(vol)
	if err != nil {
		return err
	}

	if activated {
		defer func() { _, _ = d.deactivateVolume(vol) }()
	}

	inUse := vol.MountInUse()

	// Resize filesystem if needed.
	if vol.contentType == ContentTypeFS {
		fsType := vol.ConfigBlockFilesystem()

		if sizeBytes < oldSizeBytes {
			if !filesystemTypeCanBeShrunk(fsType) {
				return fmt.Errorf("Filesystem %q cannot be shrunk: %w", fsType, ErrCannotBeShrunk)
			}

			if inUse {
				return ErrInUse // We don't allow online shrinking of filesytem volumes.
			}

			// Shrink filesystem first.
			// Pass allowUnsafeResize to allow disabling of filesystem resize safety checks.
			// We do this as a separate step rather than passing -r to lvresize in resizeLogicalVolume
			// so that we can have more control over when we trigger unsafe filesystem resize mode,
			// otherwise by passing -f to lvresize (required for other reasons) this would then pass
			// -f onto resize2fs as well.
			err = shrinkFileSystem(fsType, volDevPath, vol, sizeBytes, allowUnsafeResize)
			if err != nil {
				return err
			}

			l.Debug("Logical volume filesystem shrunk")

			// Shrink the block device.
			err = d.resizeLogicalVolume(volDevPath, sizeBytes)
			if err != nil {
				return err
			}
		} else if sizeBytes > oldSizeBytes {
			// Grow block device first.
			err = d.resizeLogicalVolume(volDevPath, sizeBytes)
			if err != nil {
				return err
			}

			// Grow the filesystem to fill block device.
			err = growFileSystem(fsType, volDevPath, vol)
			if err != nil {
				return err
			}

			l.Debug("Logical volume filesystem grown")
		}
	} else {
		// Only perform pre-resize checks if we are not in "unsafe" mode.
		// In unsafe mode we expect the caller to know what they are doing and understand the risks.
		if !allowUnsafeResize {
			if sizeBytes < oldSizeBytes {
				return fmt.Errorf("Block volumes cannot be shrunk: %w", ErrCannotBeShrunk)
			}

			if inUse {
				return ErrInUse // We don't allow online resizing of block volumes.
			}
		}

		err = d.resizeLogicalVolume(volDevPath, sizeBytes)
		if err != nil {
			return err
		}

		// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as it is
		// expected the caller will do all necessary post resize actions themselves).
		if vol.IsVMBlock() && !allowUnsafeResize {
			err = d.moveGPTAltHeader(volDevPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetVolumeDiskPath returns the location of a disk volume.
func (d *lvm) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
		return volDevPath, nil
	}

	return "", ErrNotSupported
}

// ListVolumes returns a list of volumes in storage pool.
func (d *lvm) ListVolumes() ([]Volume, error) {
	vols := make(map[string]Volume)

	cmd := exec.Command("lvs", "--noheadings", "-o", "lv_name", d.config["lvm.vg_name"])
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		rawName := strings.TrimSpace(scanner.Text())
		var volType VolumeType
		var volName string

		for _, volumeType := range d.Info().VolumeTypes {
			prefix := fmt.Sprintf("%s_", volumeType)
			if strings.HasPrefix(rawName, prefix) {
				volType = volumeType
				volName = strings.TrimPrefix(rawName, prefix)
			}
		}

		if volType == "" {
			d.logger.Debug("Ignoring unrecognised volume type", logger.Ctx{"name": rawName})
			continue // Ignore unrecognised volume.
		}

		lvSnapSepCount := strings.Count(volName, lvmSnapshotSeparator)
		if lvSnapSepCount%2 != 0 {
			// If snapshot separator count is odd, then this means we have a lone lvmSnapshotSeparator
			// that is not part of the lvmEscapedHyphen pair, which means this volume is a snapshot.
			d.logger.Debug("Ignoring snapshot volume", logger.Ctx{"name": rawName})
			continue // Ignore snapshot volumes.
		}

		isBlock := strings.HasSuffix(volName, lvmBlockVolSuffix)

		if volType == VolumeTypeVM && !isBlock {
			continue // Ignore VM filesystem volumes as we will just return the VM's block volume.
		}

		// Unescape raw LVM name to storage volume name. Safe to do now we know we are not dealing
		// with snapshot volumes.
		volName = strings.Replace(volName, lvmEscapedHyphen, "-", -1)

		contentType := ContentTypeFS
		if volType == VolumeTypeCustom && strings.HasSuffix(volName, lvmISOVolSuffix) {
			contentType = ContentTypeISO
			volName = strings.TrimSuffix(volName, lvmISOVolSuffix)
		} else if volType == VolumeTypeVM || isBlock {
			contentType = ContentTypeBlock
			volName = strings.TrimSuffix(volName, lvmBlockVolSuffix)
		}

		// If a new volume has been found, or the volume will replace an existing image filesystem volume
		// then proceed to add the volume to the map. We allow image volumes to overwrite existing
		// filesystem volumes of the same name so that for VM images we only return the block content type
		// volume (so that only the single "logical" volume is returned).
		existingVol, foundExisting := vols[volName]
		if !foundExisting || (existingVol.Type() == VolumeTypeImage && existingVol.ContentType() == ContentTypeFS) {
			v := NewVolume(d, d.name, volType, contentType, volName, make(map[string]string), d.config)

			if contentType == ContentTypeFS {
				v.SetMountFilesystemProbe(true)
			}

			vols[volName] = v
			continue
		}

		return nil, fmt.Errorf("Unexpected duplicate volume %q found", volName)
	}

	errMsg, err := io.ReadAll(stderr)
	if err != nil {
		return nil, err
	}

	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("Failed getting volume list: %v: %w", strings.TrimSpace(string(errMsg)), err)
	}

	volList := make([]Volume, len(vols))
	for _, v := range vols {
		volList = append(volList, v)
	}

	return volList, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *lvm) MountVolume(vol Volume, op *operations.Operation) error {
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	// Activate LVM volume if needed.
	activated, err := d.activateVolume(vol)
	if err != nil {
		return err
	}

	if activated {
		revert.Add(func() { _, _ = d.deactivateVolume(vol) })
	}

	if vol.contentType == ContentTypeFS {
		// Check if already mounted.
		mountPath := vol.MountPath()
		if !linux.IsMountPoint(mountPath) {
			fsType := vol.ConfigBlockFilesystem()
			volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)

			if vol.mountFilesystemProbe {
				fsType, err = fsProbe(volDevPath)
				if err != nil {
					return fmt.Errorf("Failed probing filesystem: %w", err)
				}
			}

			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}

			mountFlags, mountOptions := linux.ResolveMountOptions(strings.Split(vol.ConfigBlockMountOptions(), ","))
			err = TryMount(volDevPath, mountPath, fsType, mountFlags, mountOptions)
			if err != nil {
				return fmt.Errorf("Failed to mount LVM logical volume: %w", err)
			}

			d.logger.Debug("Mounted logical volume", logger.Ctx{"volName": vol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}
	} else if vol.contentType == ContentTypeBlock {
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
	revert.Success()
	return nil
}

// UnmountVolume unmounts volume if mounted and not in use. Returns true if this unmounted the volume.
// keepBlockDev indicates if backing block device should be not be deactivated when volume is unmounted.
func (d *lvm) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
	unlock, err := vol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := vol.MountPath()

	refCount := vol.MountRefCountDecrement()

	// Check if already mounted.
	if vol.contentType == ContentTypeFS && linux.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err = TryUnmount(mountPath, 0)
		if err != nil {
			return false, fmt.Errorf("Failed to unmount LVM logical volume: %w", err)
		}

		d.logger.Debug("Unmounted logical volume", logger.Ctx{"volName": vol.name, "path": mountPath, "keepBlockDev": keepBlockDev})

		// We only deactivate filesystem volumes if an unmount was needed to better align with our
		// unmount return value indicator.
		if !keepBlockDev {
			_, err = d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}
		}

		ourUnmount = true
	} else if vol.contentType == ContentTypeBlock {
		// For VMs, unmount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolume(fsVol, false, op)
			if err != nil {
				return false, err
			}
		}

		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
		if !keepBlockDev && util.PathExists(volDevPath) {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": vol.name, "refCount": refCount})
				return false, ErrInUse
			}

			_, err = d.deactivateVolume(vol)
			if err != nil {
				return false, err
			}

			ourUnmount = true
		}
	}

	return ourUnmount, nil
}

// RenameVolume renames a volume and its snapshots.
func (d *lvm) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)

	return vol.UnmountTask(func(op *operations.Operation) error {
		snapNames, err := d.VolumeSnapshots(vol, op)
		if err != nil {
			return err
		}

		revert := revert.New()
		defer revert.Fail()

		// Rename snapshots (change volume prefix to use new parent volume name).
		for _, snapName := range snapNames {
			snapVolName := GetSnapshotVolumeName(vol.name, snapName)
			snapVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, snapVolName)
			newSnapVolName := GetSnapshotVolumeName(newVolName, snapName)
			newSnapVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, newSnapVolName)
			err = d.renameLogicalVolume(snapVolDevPath, newSnapVolDevPath)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = d.renameLogicalVolume(newSnapVolDevPath, snapVolDevPath) })
		}

		// Rename snapshots dir if present.
		if vol.contentType == ContentTypeFS {
			srcSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, vol.name)
			dstSnapshotDir := GetVolumeSnapshotDir(d.name, vol.volType, newVolName)
			if util.PathExists(srcSnapshotDir) {
				err = os.Rename(srcSnapshotDir, dstSnapshotDir)
				if err != nil {
					return fmt.Errorf("Error renaming LVM logical volume snapshot directory from %q to %q: %w", srcSnapshotDir, dstSnapshotDir, err)
				}

				revert.Add(func() { _ = os.Rename(dstSnapshotDir, srcSnapshotDir) })
			}
		}

		// Rename actual volume.
		newVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, newVolName)
		err = d.renameLogicalVolume(volDevPath, newVolDevPath)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.renameLogicalVolume(newVolDevPath, volDevPath) })

		// Rename volume dir.
		if vol.contentType == ContentTypeFS {
			srcVolumePath := GetVolumeMountPath(d.name, vol.volType, vol.name)
			dstVolumePath := GetVolumeMountPath(d.name, vol.volType, newVolName)
			err = os.Rename(srcVolumePath, dstVolumePath)
			if err != nil {
				return fmt.Errorf("Error renaming LVM logical volume mount path from %q to %q: %w", srcVolumePath, dstVolumePath, err)
			}

			revert.Add(func() { _ = os.Rename(dstVolumePath, srcVolumePath) })
		}

		// For VMs, also rename the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			err = d.RenameVolume(fsVol, newVolName, op)
			if err != nil {
				return err
			}
		}

		revert.Success()
		return nil
	}, false, op)
}

// MigrateVolume sends a volume for migration.
func (d *lvm) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	if d.clustered && volSrcArgs.ClusterMove {
		// Mark the volume for shared locking during live migration.
		if vol.volType == VolumeTypeVM || vol.IsCustomBlock() {
			// Block volume.
			volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.Name())
			_, err := subprocess.RunCommand("lvchange", "--activate", "sy", "--ignoreactivationskip", volDevPath)
			if err != nil {
				return err
			}

			// Filesystem volume.
			if vol.IsVMBlock() {
				fsVol := vol.NewVMBlockFilesystemVolume()
				volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], fsVol.volType, fsVol.contentType, fsVol.Name())
				_, err := subprocess.RunCommand("lvchange", "--activate", "sy", "--ignoreactivationskip", volDevPath)
				if err != nil {
					return err
				}
			}
		}

		return nil // When performing a cluster member move don't do anything on the source member.
	}

	return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
}

// BackupVolume copies a volume (and optionally its snapshots) to a specified target path.
// This driver does not support optimized backups.
func (d *lvm) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, _ bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *lvm) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, snapVol.config, snapVol.poolConfig)
	snapPath := snapVol.MountPath()

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// Create snapshot directory.
	err = snapVol.EnsureMountPath()
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.RemoveAll(snapPath) })

	_, err = d.createLogicalVolumeSnapshot(d.config["lvm.vg_name"], parentVol, snapVol, true, d.usesThinpool())
	if err != nil {
		return fmt.Errorf("Error creating LVM logical volume snapshot: %w", err)
	}

	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, snapVol.name)

	revert.Add(func() {
		_ = d.removeLogicalVolume(volDevPath)
	})

	// For VMs, also snapshot the filesystem.
	if snapVol.IsVMBlock() {
		parentFSVol := parentVol.NewVMBlockFilesystemVolume()
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		_, err = d.createLogicalVolumeSnapshot(d.config["lvm.vg_name"], parentFSVol, fsVol, true, d.usesThinpool())
		if err != nil {
			return fmt.Errorf("Error creating LVM logical volume snapshot: %w", err)
		}
	}

	revert.Success()
	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device. The volName and snapshotName
// must be bare names and should not be in the format "volume/snapshot".
func (d *lvm) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	// Remove the snapshot from the storage device.
	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, snapVol.name)
	lvExists, err := d.logicalVolumeExists(volDevPath)
	if err != nil {
		return err
	}

	if lvExists {
		_, err = d.UnmountVolume(snapVol, false, op)
		if err != nil {
			return fmt.Errorf("Error unmounting LVM logical volume: %w", err)
		}

		err = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, snapVol.name))
		if err != nil {
			return fmt.Errorf("Error removing LVM logical volume: %w", err)
		}
	}

	// For VMs, also remove the snapshot filesystem volume.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		err = d.DeleteVolumeSnapshot(fsVol, op)
		if err != nil {
			return err
		}
	}

	// Remove the snapshot mount path from the storage device.
	snapPath := snapVol.MountPath()
	err = os.RemoveAll(snapPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Error removing LVM snapshot mount path %q: %w", snapPath, err)
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	return nil
}

// MountVolumeSnapshot sets up a read-only mount on top of the snapshot to avoid accidental modifications.
func (d *lvm) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	revert := revert.New()
	defer revert.Fail()

	mountPath := snapVol.MountPath()

	// Check if already mounted.
	if snapVol.contentType == ContentTypeFS && !linux.IsMountPoint(mountPath) {
		err = snapVol.EnsureMountPath()
		if err != nil {
			return err
		}

		// Default to mounting the original snapshot directly. This may be changed below if a temporary
		// snapshot needs to be taken.
		mountVol := snapVol
		mountFlags, mountOptions := linux.ResolveMountOptions(strings.Split(mountVol.ConfigBlockMountOptions(), ","))

		// Regenerate filesystem UUID if needed. This is because some filesystems do not allow mounting
		// multiple volumes that share the same UUID. As snapshotting a volume will copy its UUID we need
		// to potentially regenerate the UUID of the snapshot now that we are trying to mount it.
		// This is done at mount time rather than snapshot time for 2 reasons; firstly snapshots need to be
		// as fast as possible, and on some filesystems regenerating the UUID is a slow process, secondly
		// we do not want to modify a snapshot in case it is corrupted for some reason, so at mount time
		// we take another snapshot of the snapshot, regenerate the temporary snapshot's UUID and then
		// mount that.
		regenerateFSUUID := renegerateFilesystemUUIDNeeded(snapVol.ConfigBlockFilesystem())
		if regenerateFSUUID {
			// Instantiate a new volume to be the temporary writable snapshot.
			tmpVolName := fmt.Sprintf("%s%s", snapVol.name, tmpVolSuffix)
			tmpVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, tmpVolName, snapVol.config, snapVol.poolConfig)

			// Create writable snapshot from source snapshot named with a tmpVolSuffix suffix.
			_, err = d.createLogicalVolumeSnapshot(d.config["lvm.vg_name"], snapVol, tmpVol, false, d.usesThinpool())
			if err != nil {
				return fmt.Errorf("Error creating temporary LVM logical volume snapshot: %w", err)
			}

			revert.Add(func() {
				_ = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], tmpVol.volType, tmpVol.contentType, tmpVol.name))
			})

			// We are going to mount the temporary volume instead.
			mountVol = tmpVol
		}

		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], mountVol.volType, mountVol.contentType, mountVol.name)

		// Activate volume if needed.
		_, err = d.activateVolume(mountVol)
		if err != nil {
			return err
		}

		if regenerateFSUUID {
			tmpVolFsType := mountVol.ConfigBlockFilesystem()

			// When mounting XFS filesystems temporarily we can use the nouuid option rather than fully
			// regenerating the filesystem UUID.
			if tmpVolFsType == "xfs" {
				idx := strings.Index(mountOptions, "nouuid")
				if idx < 0 {
					mountOptions += ",nouuid"
				}
			} else {
				d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volDevPath, "fs": tmpVolFsType})
				err = regenerateFilesystemUUID(mountVol.ConfigBlockFilesystem(), volDevPath)
				if err != nil {
					return err
				}
			}
		}

		// Finally attempt to mount the volume that needs mounting.
		err = TryMount(volDevPath, mountPath, mountVol.ConfigBlockFilesystem(), mountFlags|unix.MS_RDONLY, mountOptions)
		if err != nil {
			return fmt.Errorf("Failed to mount LVM snapshot volume: %w", err)
		}

		d.logger.Debug("Mounted logical volume snapshot", logger.Ctx{"dev": volDevPath, "path": mountPath, "options": mountOptions})
	} else if snapVol.contentType == ContentTypeBlock {
		// Activate volume if needed.
		_, err = d.activateVolume(snapVol)
		if err != nil {
			return err
		}

		// For VMs, mount the filesystem volume.
		if snapVol.IsVMBlock() {
			fsVol := snapVol.NewVMBlockFilesystemVolume()
			err = d.MountVolumeSnapshot(fsVol, op)
			if err != nil {
				return err
			}
		}
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	revert.Success()
	return nil
}

// UnmountVolumeSnapshot removes the read-only mount placed on top of a snapshot.
// If a temporary snapshot volume exists then it will attempt to remove it.
func (d *lvm) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	unlock, err := snapVol.MountLock()
	if err != nil {
		return false, err
	}

	defer unlock()

	ourUnmount := false
	mountPath := snapVol.MountPath()

	refCount := snapVol.MountRefCountDecrement()

	// Check if already mounted.
	if snapVol.contentType == ContentTypeFS && linux.IsMountPoint(mountPath) {
		if refCount > 0 {
			d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
			return false, ErrInUse
		}

		err = TryUnmount(mountPath, 0)
		if err != nil {
			return false, fmt.Errorf("Failed to unmount LVM snapshot volume: %w", err)
		}

		d.logger.Debug("Unmounted logical volume snapshot", logger.Ctx{"path": mountPath})

		// Check if a temporary snapshot exists, and if so remove it.
		tmpVolName := fmt.Sprintf("%s%s", snapVol.name, tmpVolSuffix)
		tmpVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, tmpVolName)
		exists, err := d.logicalVolumeExists(tmpVolDevPath)
		if err != nil {
			return true, fmt.Errorf("Failed to check existence of temporary LVM snapshot volume %q: %w", tmpVolDevPath, err)
		}

		if exists {
			err = d.removeLogicalVolume(tmpVolDevPath)
			if err != nil {
				return true, fmt.Errorf("Failed to remove temporary LVM snapshot volume %q: %w", tmpVolDevPath, err)
			}
		}

		// We only deactivate filesystem volumes if an unmount was needed to better align with our
		// unmount return value indicator.
		_, err = d.deactivateVolume(snapVol)
		if err != nil {
			return false, err
		}

		ourUnmount = true
	} else if snapVol.contentType == ContentTypeBlock {
		// For VMs, unmount the filesystem volume.
		if snapVol.IsVMBlock() {
			fsVol := snapVol.NewVMBlockFilesystemVolume()
			ourUnmount, err = d.UnmountVolumeSnapshot(fsVol, op)
			if err != nil {
				return false, err
			}
		}

		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, snapVol.name)
		if util.PathExists(volDevPath) {
			if refCount > 0 {
				d.logger.Debug("Skipping unmount as in use", logger.Ctx{"volName": snapVol.name, "refCount": refCount})
				return false, ErrInUse
			}

			_, err = d.deactivateVolume(snapVol)
			if err != nil {
				return false, err
			}

			ourUnmount = true
		}
	}

	return ourUnmount, nil
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *lvm) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	// We use the volume list rather than inspecting the logical volumes themselves because the origin
	// property of an LVM snapshot can be removed/changed when restoring snapshots, such that they are no
	// marked as origin of the parent volume. Instead we use prefix matching on the volume names to find the
	// snapshot volumes.
	cmd := exec.Command("lvs", "--noheadings", "-o", "lv_name", d.config["lvm.vg_name"])
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	snapshots := []string{}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		snapName := d.parseLogicalVolumeSnapshot(vol, strings.TrimSpace(scanner.Text()))
		if snapName == "" {
			continue // Skip logical volumes that are not recognised as a snapshot of our parent vol.
		}

		snapshots = append(snapshots, snapName)
	}

	errMsg, err := io.ReadAll(stderr)
	if err != nil {
		return nil, err
	}

	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("Failed to get snapshot list for volume %q: %v: %w", vol.name, strings.TrimSpace(string(errMsg)), err)
	}

	return snapshots, nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *lvm) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	// Instantiate snapshot volume from snapshot name.
	snapVol, err := vol.NewSnapshot(snapshotName)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// If the pool uses thinpools, then the process for restoring a snapshot is as follows:
	// 1. Rename the original volume to a temporary name (so we can revert later if needed).
	// 2. Create a writable snapshot with the original name from the snapshot being restored.
	// 3. Delete the renamed original volume.
	if d.usesThinpool() {
		_, err = d.UnmountVolume(vol, false, op)
		if err != nil {
			return fmt.Errorf("Error unmounting LVM logical volume: %w", err)
		}

		originalVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)
		tmpVolName := fmt.Sprintf("%s%s", vol.name, tmpVolSuffix)
		tmpVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, tmpVolName)

		// Rename original logical volume to temporary new name so we can revert if needed.
		err = d.renameLogicalVolume(originalVolDevPath, tmpVolDevPath)
		if err != nil {
			return fmt.Errorf("Error temporarily renaming original LVM logical volume: %w", err)
		}

		revert.Add(func() {
			// Rename the original volume back to the original name.
			_ = d.renameLogicalVolume(tmpVolDevPath, originalVolDevPath)
		})

		// Create writable snapshot from source snapshot named as target volume.
		_, err = d.createLogicalVolumeSnapshot(d.config["lvm.vg_name"], snapVol, vol, false, true)
		if err != nil {
			return fmt.Errorf("Error restoring LVM logical volume snapshot: %w", err)
		}

		volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, vol.name)

		revert.Add(func() {
			_ = d.removeLogicalVolume(volDevPath)
		})

		// If the volume's filesystem needs to have its UUID regenerated to allow mount then do so now.
		if vol.contentType == ContentTypeFS && renegerateFilesystemUUIDNeeded(vol.ConfigBlockFilesystem()) {
			_, err = d.activateVolume(vol)
			if err != nil {
				return err
			}

			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": volDevPath, "fs": vol.ConfigBlockFilesystem()})
			err = regenerateFilesystemUUID(vol.ConfigBlockFilesystem(), volDevPath)
			if err != nil {
				return err
			}
		}

		// Finally remove the original logical volume. Should always be the last step to allow revert.
		err = d.removeLogicalVolume(d.lvmDevPath(d.config["lvm.vg_name"], vol.volType, vol.contentType, tmpVolName))
		if err != nil {
			return fmt.Errorf("Error removing original LVM logical volume: %w", err)
		}

		revert.Success()
		return nil
	}

	// If the pool uses classic logical volumes, then the process for restoring a snapshot is as follows:
	// 1. Ensure snapshot volumes have sufficient CoW capacity to allow restoration.
	// 2. Mount source and target.
	// 3. Copy (rsync or dd) source to target.
	// 4. Unmount source and target.

	// Ensure that the snapshot volumes have sufficient CoW capacity to allow restoration.
	// In the past we set snapshot sizes by specifying the same size as the origin volume. Unfortunately due to
	// the way that LVM extents work, this means that the snapshot CoW capacity can be just a little bit too
	// small to allow the entire snapshot to be restored to the origin. If this happens then we can end up
	// invalidating the snapshot meaning it cannot be used anymore!
	// Nowadays we use the "100%ORIGIN" size when creating snapshots, which lets LVM figure out what the number
	// of extents is required to restore the whole snapshot, but we need to support resizing older snapshots
	// taken before this change. So we use lvresize here to grow the snapshot volume to the size of the origin.
	// The use of "+100%ORIGIN" here rather than just "100%ORIGIN" like we use when taking new snapshots, is
	// rather counter intuitive. However there seems to be a bug in lvresize/lvextend so that when specifying
	// "100%ORIGIN", it fails to extend sufficiently, saying that the number of extents in the snapshot matches
	// that of the origin (which they do). However if we take take that at face value then the restore will
	// end up invalidating the snapshot. Instead if we specify a much larger value (such as adding 100% of
	// the origin to the snapshot size) then LVM is able to extend the snapshot a little bit more, and LVM
	// limits the new size to the maximum CoW size that the snapshot can be (which happens to be the same size
	// as newer snapshots are taken at using the "100%ORIGIN" size). Confusing isn't it.
	if snapVol.IsVMBlock() || snapVol.contentType == ContentTypeFS {
		snapLVPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, ContentTypeFS, snapVol.name)
		_, err = subprocess.TryRunCommand("lvresize", "-l", "+100%ORIGIN", "-f", snapLVPath)
		if err != nil {
			return err
		}
	}

	if snapVol.IsVMBlock() || (snapVol.contentType == ContentTypeBlock && snapVol.volType == VolumeTypeCustom) {
		snapLVPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, ContentTypeBlock, snapVol.name)
		_, err = subprocess.TryRunCommand("lvresize", "-l", "+100%ORIGIN", "-f", snapLVPath)
		if err != nil {
			return err
		}
	}

	// Mount source and target, copy, then unmount.
	err = vol.MountTask(func(mountPath string, op *operations.Operation) error {
		// Copy source to destination (mounting each volume if needed).
		err = snapVol.MountTask(func(srcMountPath string, op *operations.Operation) error {
			if snapVol.IsVMBlock() || snapVol.contentType == ContentTypeFS {
				bwlimit := d.config["rsync.bwlimit"]
				d.Logger().Debug("Copying fileystem volume", logger.Ctx{"sourcePath": srcMountPath, "targetPath": mountPath, "bwlimit": bwlimit})
				_, err := rsync.LocalCopy(srcMountPath, mountPath, bwlimit, true)
				if err != nil {
					return err
				}
			}

			if snapVol.IsVMBlock() || (snapVol.contentType == ContentTypeBlock && snapVol.volType == VolumeTypeCustom) {
				srcDevPath, err := d.GetVolumeDiskPath(snapVol)
				if err != nil {
					return err
				}

				targetDevPath, err := d.GetVolumeDiskPath(vol)
				if err != nil {
					return err
				}

				d.Logger().Debug("Copying block volume", logger.Ctx{"srcDevPath": srcDevPath, "targetPath": targetDevPath})
				err = copyDevice(srcDevPath, targetDevPath)
				if err != nil {
					return err
				}
			}

			return nil
		}, op)
		if err != nil {
			return err
		}

		// Run EnsureMountPath after mounting and syncing to ensure the mounted directory has the
		// correct permissions set.
		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		return nil
	}, op)
	if err != nil {
		return fmt.Errorf("Error restoring LVM logical volume snapshot: %w", err)
	}

	revert.Success()
	return nil
}

// RenameVolumeSnapshot renames a volume snapshot.
func (d *lvm) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	volDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, snapVol.name)

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	newSnapVolName := GetSnapshotVolumeName(parentName, newSnapshotName)
	newVolDevPath := d.lvmDevPath(d.config["lvm.vg_name"], snapVol.volType, snapVol.contentType, newSnapVolName)
	err := d.renameLogicalVolume(volDevPath, newVolDevPath)
	if err != nil {
		return fmt.Errorf("Error renaming LVM logical volume: %w", err)
	}

	oldPath := snapVol.MountPath()
	newPath := GetVolumeMountPath(d.name, snapVol.volType, newSnapVolName)
	err = os.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("Error renaming snapshot mount path from %q to %q: %w", oldPath, newPath, err)
	}

	return nil
}
