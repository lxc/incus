package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	linstorClient "github.com/LINBIT/golinstor/client"
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

// FillVolumeConfig populate volume with default config.
func (d *linstor) FillVolumeConfig(vol Volume) error {
	// Copy volume.* configuration options from pool.
	// Exclude 'block.filesystem' and 'block.mount_options'
	// as this ones are handled below in this function and depends from volume type.
	err := d.fillVolumeConfig(&vol, "block.filesystem", "block.mount_options")
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

	return nil
}

// commonVolumeRules returns validation rules which are common for pool and volume.
func (d *linstor) commonVolumeRules() map[string]func(value string) error {
	return map[string]func(value string) error{
		"block.filesystem":    validate.Optional(validate.IsOneOf(blockBackedAllowedFilesystems...)),
		"block.mount_options": validate.IsAny,
	}
}

// ValidateVolume validates the supplied volume config.
func (d *linstor) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
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

// CreateVolume creates an empty volume and can optionally fill it by executing the supplied
// filler function.
func (d *linstor) CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Creating a new Linstor volume")
	rev := revert.New()
	defer rev.Fail()

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	if vol.contentType == ContentTypeFS {
		// Create mountpoint.
		err := vol.EnsureMountPath()
		if err != nil {
			return err
		}

		rev.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// Transform byte to KiB.
	requiredBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return fmt.Errorf("Unable to parse volume size: %w", err)
	}

	requiredKiB := requiredBytes / 1024
	resourceDefinitionName := d.generateUUIDWithPrefix()

	volumeSizes := []int64{requiredKiB}
	// For VMs, create and extra volume on the resource for the filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()

		// Transform byte to KiB.
		requiredBytes, err := units.ParseByteSizeString(fsVol.ConfigSize())
		if err != nil {
			return fmt.Errorf("Unable to parse volume size: %w", err)
		}

		requiredKiB := requiredBytes / 1024

		volumeSizes = append(volumeSizes, requiredKiB)
	}

	// Spawn resource.
	err = linstor.Client.ResourceGroups.Spawn(context.TODO(), d.config[LinstorResourceGroupNameConfigKey], linstorClient.ResourceGroupSpawn{
		ResourceDefinitionName: resourceDefinitionName,
		VolumeSizes:            volumeSizes,
	})
	if err != nil {
		return fmt.Errorf("Unable to spawn from resource group: %w", err)
	}

	l.Debug("Spawned a new Linstor resource definition for volume", logger.Ctx{"resourceDefinitionName": resourceDefinitionName})
	rev.Add(func() { _ = d.DeleteVolume(vol, op) })

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinitionName, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{
			LinstorAuxName:                        d.config[LinstorVolumePrefixConfigKey] + vol.name,
			LinstorAuxType:                        string(vol.volType),
			LinstorAuxContentType:                 string(vol.contentType),
			"DrbdOptions/Net/allow-two-primaries": "yes",
		},
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	// Setup the filesystem.
	if vol.contentType == ContentTypeFS {
		devPath, err := d.getLinstorDevPath(vol)
		if err != nil {
			return fmt.Errorf("Could not get device path for filesystem creation: %w", err)
		}

		volFilesystem := vol.ConfigBlockFilesystem()

		_, err = makeFSType(devPath, volFilesystem, nil)
		if err != nil {
			return err
		}
	}

	// For VMs, also create the filesystem on the associated filesystem volume.
	if vol.IsVMBlock() {
		l.Debug("Creating filesystem on the associated filesystem volume")

		fsVol := vol.NewVMBlockFilesystemVolume()
		fsVolDevPath, err := d.getLinstorDevPath(fsVol)
		if err != nil {
			return fmt.Errorf("Could not get device path for filesystem creation: %w", err)
		}

		fsVolFilesystem := fsVol.ConfigBlockFilesystem()
		_, err = makeFSType(fsVolDevPath, fsVolFilesystem, nil)

		l.Debug("Created filesystem on the associated filesystem volume", logger.Ctx{"fsVolDevPath": fsVolDevPath, "fsVolFilesystem": fsVolFilesystem})
		if err != nil {
			return err
		}
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
			err = vol.EnsureMountPath()
			if err != nil {
				return err
			}
		}

		return nil
	}, op)
	if err != nil {
		return nil
	}

	rev.Success()
	return nil
}

// CreateVolumeFromCopy provides same-pool volume copying functionality.
func (d *linstor) CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"vol": vol.Name(), "srcVol": srcVol.Name()})
	l.Debug("Creating Linstor volume from copy")
	rev := revert.New()
	defer rev.Fail()

	// For snapshots, restore them into the target volume.
	if srcVol.IsSnapshot() {
		l.Debug("Copying snapshot to volume")
		err := d.createResourceDefinitionFromSnapshot(srcVol, vol)
		if err != nil {
			return err
		}

		return nil
	}

	if copySnapshots {
		// Get the list of snapshots from the source.
		srcSnapshots, err := srcVol.Snapshots(op)
		if err != nil {
			return err
		}

		if len(srcSnapshots) > 0 {
			l.Debug("Snapshots copying required. Falling back to generic copy implementation")
			// Ensure the mount path for ISO volumes is created when using the generic
			// copy implementation. This is needed because genericVFSCopyVolume treats
			// ISO volumes like filesystem volumes when performing the copy. This implies
			// that the mount path for the volume must exist before the copying starts.
			if srcVol.contentType == ContentTypeISO {
				err := srcVol.EnsureMountPath()
				if err != nil {
					return err
				}

				rev.Add(func() { _ = os.Remove(vol.MountPath()) })
			}

			// TODO: support optimized copying with snapshots
			return genericVFSCopyVolume(d, nil, vol, srcVol, srcSnapshots, false, allowInconsistent, op)
		}
	}

	// For VM volumes, the associated filesystem volume is already cloned with the main block
	// volume, since they share the same resource definition.
	if vol.volType != VolumeTypeVM || vol.contentType != ContentTypeFS {
		err := d.copyVolume(vol, srcVol)
		if err != nil {
			return err
		}

		rev.Add(func() { _ = d.DeleteVolume(vol, op) })
	}

	if vol.contentType == ContentTypeFS {
		devPath, err := d.getLinstorDevPath(vol)
		if err != nil {
			return err
		}

		fsType := vol.ConfigBlockFilesystem()

		// Generate a new filesystem UUID if needed (this is required because some filesystems won't allow
		// volumes with the same UUID to be mounted at the same time). This should be done before volume
		// resize as some filesystems will need to mount the filesystem to resize.
		if renegerateFilesystemUUIDNeeded(fsType) {
			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"dev": devPath, "fs": fsType})
			err = regenerateFilesystemUUID(fsType, devPath)
			if err != nil {
				return err
			}
		}

		// Create mountpoint.
		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		rev.Add(func() { _ = os.Remove(vol.MountPath()) })
	}

	// Resize volume to the size specified. Only uses volume "size" property and does not use
	// pool/defaults to give the caller more control over the size being used.
	err := d.SetVolumeQuota(vol, vol.config["size"], false, op)
	if err != nil {
		return err
	}

	// For VMs, also copy the filesystem volume.
	if vol.IsVMBlock() {
		srcFSVol := srcVol.NewVMBlockFilesystemVolume()
		fsVol := vol.NewVMBlockFilesystemVolume()

		err = d.CreateVolumeFromCopy(fsVol, srcFSVol, copySnapshots, false, op)
		if err != nil {
			return err
		}
	}

	rev.Success()

	return nil
}

// RefreshVolume updates an existing volume to match the state of another.
func (d *linstor) RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, allowInconsistent bool, op *operations.Operation) error {
	return genericVFSCopyVolume(d, nil, vol, srcVol, srcSnapshots, true, allowInconsistent, op)
}

// DeleteVolume deletes a volume of the storage device.
func (d *linstor) DeleteVolume(vol Volume, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Deleting Linstor volume")

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	// Test if the volume exists.
	volumeExists, err := d.HasVolume(vol)
	if err != nil {
		return fmt.Errorf("Unable to check if volume exists: %w", err)
	}

	if !volumeExists {
		l.Warn("Resource definition does not exist")
	} else {
		resourceDefinition, err := d.getResourceDefinition(vol, false)
		if err != nil {
			return err
		}

		err = linstor.Client.ResourceDefinitions.Delete(context.TODO(), resourceDefinition.Name)
		if err != nil {
			return fmt.Errorf("Unable to delete the resource definition: %w", err)
		}
	}

	// For VMs, also delete the associated filesystem volume mount path.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		fsVolMountPath := fsVol.MountPath()

		err := wipeDirectory(fsVolMountPath)
		if err != nil {
			return err
		}

		err = os.Remove(fsVolMountPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", fsVolMountPath, err)
		}
	}

	mountPath := vol.MountPath()

	if vol.contentType == ContentTypeFS && util.PathExists(mountPath) {
		err := wipeDirectory(mountPath)
		if err != nil {
			return err
		}

		err = os.Remove(mountPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", mountPath, err)
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *linstor) HasVolume(vol Volume) (bool, error) {
	if vol.IsSnapshot() {
		parentName, _, _ := api.GetParentAndSnapshotName(vol.name)
		parentVol := NewVolume(d, d.name, vol.volType, vol.contentType, parentName, nil, nil)

		return d.snapshotExists(parentVol, vol)
	}

	_, err := d.getResourceDefinition(vol, false)
	if err != nil {
		if errors.Is(err, errResourceDefinitionNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// GetVolumeDiskPath returns the location of a root disk block device.
func (d *linstor) GetVolumeDiskPath(vol Volume) (string, error) {
	if vol.IsVMBlock() || (vol.volType == VolumeTypeCustom && IsContentBlock(vol.contentType)) {
		devPath, err := d.getLinstorDevPath(vol)
		return devPath, err
	}

	return "", ErrNotSupported
}

// ListVolumes returns a list of volumes in storage pool.
func (d *linstor) ListVolumes() ([]Volume, error) {
	d.logger.Debug("Listing volumes")

	resourceDefinitions, err := d.getResourceDefinitions()
	if err != nil {
		return nil, err
	}

	var volumes []Volume

	for _, rd := range resourceDefinitions {
		l := d.logger.AddContext(logger.Ctx{"resourceDefinition": rd.Name})
		if rd.ResourceGroupName != d.config[LinstorResourceGroupNameConfigKey] {
			l.Debug("Ignoring resource definition not linked to the storage pool's resource group")
			continue
		}

		volName, ok := rd.Props[LinstorAuxName]
		if !ok {
			l.Debug("Ignoring resource definition with no name aux property set")
			continue
		}

		// Strip the volume prefix.
		volName = strings.TrimPrefix(volName, d.config[LinstorVolumePrefixConfigKey])

		rawVolType, ok := rd.Props[LinstorAuxType]
		if !ok {
			l.Debug("Ignoring resource definition with no type aux property set")
			continue
		}

		volType, ok := d.parseVolumeType(rawVolType)
		if !ok && volType != nil {
			l.Debug("Ignoring resource definition with invalid type")
			continue
		}

		rawContentType, ok := rd.Props[LinstorAuxContentType]
		if !ok {
			l.Debug("Ignoring resource definition with no name contentType property set")
			continue
		}

		contentType, ok := d.parseContentType(rawContentType)
		if !ok && contentType != nil {
			l.Debug("Ignoring resource definition with invalid contentType")
			continue
		}

		vol := NewVolume(d, d.name, *volType, *contentType, volName, make(map[string]string), d.config)
		volumes = append(volumes, vol)

		l.Debug("Found volume from storage pool", logger.Ctx{"volName": volName, "volType": *volType, "contentType": *contentType})
	}

	return volumes, nil
}

// MountVolume mounts a volume and increments ref counter. Please call UnmountVolume() when done with the volume.
func (d *linstor) MountVolume(vol Volume, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Mounting volume")
	unlock, err := vol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	rev := revert.New()
	defer rev.Fail()

	volDevPath, err := d.getLinstorDevPath(vol)
	if err != nil {
		return fmt.Errorf("Could not mount volume: %w", err)
	}

	l.Debug("Volume is available on node", logger.Ctx{"volDevPath": volDevPath})

	switch vol.contentType {
	case ContentTypeFS:
		mountPath := vol.MountPath()
		l.Debug("Content type FS", logger.Ctx{"mountPath": mountPath})
		if !linux.IsMountPoint(mountPath) {
			err := vol.EnsureMountPath()
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
			l.Debug("Will try mount", logger.Ctx{"mountFlags": mountFlags, "mountOptions": mountOptions})
			err = TryMount(volDevPath, mountPath, fsType, mountFlags, mountOptions)
			if err != nil {
				l.Debug("Tried mounting but failed", logger.Ctx{"error": err})
				return err
			}

			d.logger.Debug("Mounted Linstor volume", logger.Ctx{"volName": vol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}

	case ContentTypeBlock:
		l.Debug("Content type Block")
		// For VMs, mount the filesystem volume.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()
			l.Debug("Created a new FS volume", logger.Ctx{"fsVol": fsVol})
			err = d.MountVolume(fsVol, op)
			if err != nil {
				l.Debug("Tried mounting but failed", logger.Ctx{"error": err})
				return err
			}
		}
	}

	vol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolume() when done.
	rev.Success()
	l.Debug("Volume mounted")
	return nil
}

// UnmountVolume clears any runtime state for the volume.
// keepBlockDev indicates if backing block device should be not be unmapped if volume is unmounted.
func (d *linstor) UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error) {
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

		err = TryUnmount(mountPath, unix.MNT_DETACH)
		if err != nil {
			return false, err
		}

		d.logger.Debug("Unmounted Linstor volume", logger.Ctx{"volName": vol.name, "path": mountPath, "keepBlockDev": keepBlockDev})

		// TODO: handle keepBlockDev

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

		// TODO: handle keepBlockDev
	}

	return ourUnmount, nil
}

// RenameVolume renames a volume.
func (d *linstor) RenameVolume(vol Volume, newVolName string, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Renaming Linstor volume")
	rev := revert.New()
	defer rev.Fail()

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinition.Name, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{
			LinstorAuxName: d.config[LinstorVolumePrefixConfigKey] + newVolName,
		},
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	return nil
}

// CreateVolumeSnapshot creates a snapshot of a volume.
func (d *linstor) CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	rev := revert.New()
	defer rev.Fail()

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)

	// Create the parent directory.
	err := createParentSnapshotDirIfMissing(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	err = snapVol.EnsureMountPath()
	if err != nil {
		return err
	}

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	err = d.createVolumeSnapshot(parentVol, snapVol)
	if err != nil {
		return err
	}

	rev.Add(func() { _ = d.DeleteVolumeSnapshot(snapVol, op) })

	rev.Success()

	return nil
}

// DeleteVolumeSnapshot removes a snapshot from the storage device.
func (d *linstor) DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)

	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	snapshotExists, err := d.snapshotExists(parentVol, snapVol)
	if err != nil {
		return fmt.Errorf("Failed to delete volume snapshot: %w", err)
	}

	// Check if snapshot exists, and return if not.
	if !snapshotExists {
		return nil
	}

	err = d.deleteVolumeSnapshot(parentVol, snapVol)
	if err != nil {
		return fmt.Errorf("Failed to delete volume snapshot: %w", err)
	}

	mountPath := snapVol.MountPath()

	if snapVol.contentType == ContentTypeFS && util.PathExists(mountPath) {
		err = wipeDirectory(mountPath)
		if err != nil {
			return err
		}

		err = os.Remove(mountPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", mountPath, err)
		}
	}

	// Remove the parent snapshot directory if this is the last snapshot being removed.
	err = deleteParentSnapshotDirIfEmpty(d.name, snapVol.volType, parentName)
	if err != nil {
		return err
	}

	return nil
}

// RestoreVolume restores a volume from a snapshot.
func (d *linstor) RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error {
	ourUnmount, err := d.UnmountVolume(vol, false, op)
	if err != nil {
		return err
	}

	if ourUnmount {
		defer func() { _ = d.MountVolume(vol, op) }()
	}

	snapVol, err := vol.NewSnapshot(snapshotName)
	if err != nil {
		return err
	}

	// TODO: check if more recent snapshots exist and delete them if the user
	// configure the storage pool to allow for such deletions. Otherwise, return
	// a graceful error
	err = d.restoreVolume(vol, snapVol)
	if err != nil {
		return err
	}

	return nil
}

// RenameVolumeSnapshot is a no-op.
func (d *linstor) RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error {
	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)
	return d.renameVolumeSnapshot(parentVol, snapVol, newSnapshotName)
}

// MountVolumeSnapshot mounts a storage volume snapshot.
//
// The snapshot is restored into a new temporary LINSTOR resource definition that will live for the duration of the mount.
func (d *linstor) MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": snapVol.Name()})
	l.Debug("Mounting snapshot volume")

	unlock, err := snapVol.MountLock()
	if err != nil {
		return err
	}

	defer unlock()

	rev := revert.New()
	defer rev.Fail()

	// For VMs, mount the filesystem volume.
	if snapVol.IsVMBlock() {
		fsVol := snapVol.NewVMBlockFilesystemVolume()
		l.Debug("Created a new FS volume", logger.Ctx{"fsVol": fsVol})
		return d.MountVolumeSnapshot(fsVol, op)
	}

	// Create a new temporary resource-definition from the snapshot
	err = d.createResourceFromSnapshot(snapVol, snapVol)
	if err != nil {
		return err
	}

	rev.Add(func() { _ = d.DeleteVolume(snapVol, op) })

	volDevPath, err := d.getLinstorDevPath(snapVol)
	if err != nil {
		return fmt.Errorf("Could not mount volume: %w", err)
	}

	l.Debug("Volume is available on node", logger.Ctx{"volDevPath": volDevPath})

	if snapVol.contentType == ContentTypeFS {
		mountPath := snapVol.MountPath()
		l.Debug("Content type FS", logger.Ctx{"mountPath": mountPath})
		if !linux.IsMountPoint(mountPath) {
			err := snapVol.EnsureMountPath()
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

			d.logger.Debug("Regenerating filesystem UUID", logger.Ctx{"volDevPath": volDevPath, "fs": snapVolFS})
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

			d.logger.Debug("Mounted snapshot volume", logger.Ctx{"volName": snapVol.name, "dev": volDevPath, "path": mountPath, "options": mountOptions})
		}
	}

	snapVol.MountRefCountIncrement() // From here on it is up to caller to call UnmountVolumeSnapshot() when done.
	rev.Success()
	return nil
}

// UnmountVolumeSnapshot unmounts a volume snapshot.
//
// The temporary LINSTOR resource definition created for the mount is deleted.
func (d *linstor) UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error) {
	l := d.logger.AddContext(logger.Ctx{"volume": snapVol.Name()})
	l.Debug("Umounting snapshot volume")

	linstor, err := d.state.Linstor()
	if err != nil {
		return false, err
	}

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

		ourUnmount, err = forceUnmount(mountPath)
		if err != nil {
			return false, err
		}

		l.Debug("Unmounted snapshot volume filesystem", logger.Ctx{"path": mountPath})
	}

	l.Debug("Deleting temporary resource definition for snapshot mount")

	resourceDefinition, err := d.getResourceDefinition(snapVol, false)
	if err != nil {
		if errors.Is(err, errResourceDefinitionNotFound) {
			return false, nil
		}

		return false, err
	}

	err = linstor.Client.ResourceDefinitions.Delete(context.TODO(), resourceDefinition.Name)
	if err != nil {
		return false, fmt.Errorf("Could not delete temporary resource definition: %w", err)
	}

	d.logger.Debug("Temporary resource definition deleted")

	return ourUnmount, nil
}

// UpdateVolume applies config changes to the volume.
func (d *linstor) UpdateVolume(vol Volume, changedConfig map[string]string) error {
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
func (d *linstor) GetVolumeUsage(vol Volume) (int64, error) {
	usageInKiB, err := d.getVolumeUsage(vol)
	if err != nil {
		return 0, fmt.Errorf("Could not get volume usage: %w", err)
	}

	usageInBytes := usageInKiB * 1024
	return usageInBytes, nil
}

// SetVolumeQuota applies a size limit on volume.
// Does nothing if supplied with an empty/zero size.
func (d *linstor) SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Setting volume quota")

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	// Do nothing if size isn't specified.
	if sizeBytes <= 0 {
		return nil
	}

	// Get the device path.
	devPath, err := d.getLinstorDevPath(vol)
	if err != nil {
		return err
	}

	// LVM and ZFS (LINSTOR's backends) round up the final block device size to the next extent. Because of
	// this, we cannot simply get the volume size from the OS. We must fetch the size from LINSTOR.
	oldSizeBytes, err := d.getVolumeSize(vol)
	if err != nil {
		return fmt.Errorf("Error getting current size: %w", err)
	}

	// Do nothing if volume is already specified size (+/- 512 bytes).
	if oldSizeBytes+512 > sizeBytes && oldSizeBytes-512 < sizeBytes {
		return nil
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
				return ErrInUse // We don't allow online shrinking of filesystem volumes.
			}

			// Shrink filesystem first. Pass allowUnsafeResize to allow disabling of filesystem
			// resize safety checks.
			err = shrinkFileSystem(fsType, devPath, vol, sizeBytes, allowUnsafeResize)
			if err != nil {
				return err
			}

			// Shrink the block device.
			err = d.resizeVolume(vol, sizeBytes)
			if err != nil {
				return err
			}
		} else if sizeBytes > oldSizeBytes {
			// Grow block device first.
			err = d.resizeVolume(vol, sizeBytes)
			if err != nil {
				return err
			}

			// Grow the filesystem to fill block device.
			err = growFileSystem(fsType, devPath, vol)
			if err != nil {
				return err
			}
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

		// Resize block device.
		err = d.resizeVolume(vol, sizeBytes)
		if err != nil {
			return err
		}

		// Move the VM GPT alt header to end of disk if needed (not needed in unsafe resize mode as it is
		// expected the caller will do all necessary post resize actions themselves).
		if vol.IsVMBlock() && !allowUnsafeResize {
			err = d.moveGPTAltHeader(devPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// MigrateVolume sends a volume for migration.
func (d *linstor) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *localMigration.VolumeSourceArgs, op *operations.Operation) error {
	d.logger.Debug("Migrating volume", logger.Ctx{"volume": vol.Name(), "volSrcArgs": volSrcArgs})

	// When migrating between cluster members on the same storage pool, don't do anything on the source member.
	if volSrcArgs.ClusterMove && !volSrcArgs.StorageMove {
		d.logger.Debug("Detected migration between cluster members on the same storage pool", logger.Ctx{"volume": vol.Name(), "volSrcArgs": volSrcArgs})
		return nil
	}

	// Handle simple rsync and block_and_rsync through generic.
	if volSrcArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volSrcArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		// TODO: create a fast snapshot to ensure migration consistency when the driver supports snapshots
		parent, _, _ := api.GetParentAndSnapshotName(vol.Name())
		parentVol := NewVolume(d, d.Name(), vol.volType, vol.contentType, parent, vol.config, vol.poolConfig)
		err := d.MountVolume(parentVol, op)
		if err != nil {
			return err
		}

		defer func() { _, _ = d.UnmountVolume(parentVol, false, op) }()

		return genericVFSMigrateVolume(d, d.state, vol, conn, volSrcArgs, op)
	} else if volSrcArgs.MigrationType.FSType != migration.MigrationFSType_LINSTOR {
		return ErrNotSupported
	}

	// TODO: handle optimize migration to other LINSTOR storage pools
	return ErrNotSupported
}

// CreateVolumeFromMigration creates a volume being sent via a migration.
func (d *linstor) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs localMigration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	d.logger.Debug("Receiving volume from migration", logger.Ctx{"volume": vol.Name(), "volTargetArgs": volTargetArgs})
	if volTargetArgs.ClusterMoveSourceName != "" && volTargetArgs.StoragePool == "" {
		d.logger.Debug("Detected migration between cluster members on the same storage pool", logger.Ctx{"volume": vol.Name(), "volTargetArgs": volTargetArgs})

		err := d.makeVolumeAvailable(vol)
		if err != nil {
			return err
		}

		err = vol.EnsureMountPath()
		if err != nil {
			return err
		}

		// For VMs, the associated filesystem volume is contained within the resource, so it
		// is migrated in the same operation. The only thing left to do is to ensure that its
		// mount path exists on the host.
		if vol.IsVMBlock() {
			fsVol := vol.NewVMBlockFilesystemVolume()

			err = fsVol.EnsureMountPath()
			if err != nil {
				return err
			}
		}

		d.logger.Debug("Finished migrating", logger.Ctx{"volume": vol.Name(), "volTargetArgs": volTargetArgs})
		return nil
	}

	// Handle simple rsync and block_and_rsync through generic.
	if volTargetArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volTargetArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC {
		return genericVFSCreateVolumeFromMigration(d, nil, vol, conn, volTargetArgs, preFiller, op)
	} else if volTargetArgs.MigrationType.FSType != migration.MigrationFSType_LINSTOR {
		return ErrNotSupported
	}

	// TODO: handle optimize migration from other LINSTOR storage pools
	return ErrNotSupported
}

// VolumeSnapshots returns a list of snapshots for the volume (in no particular order).
func (d *linstor) VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error) {
	var snapshots []string

	linstor, err := d.state.Linstor()
	if err != nil {
		return snapshots, err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return snapshots, err
	}

	snapshotMap, err := d.getSnapshotMap(vol)
	if err != nil {
		return snapshots, err
	}

	// Get the snapshots.
	linstorSnapshots, err := linstor.Client.Resources.GetSnapshots(context.TODO(), resourceDefinition.Name)
	if err != nil {
		return snapshots, fmt.Errorf("Unable to get snapshots: %w", err)
	}

	for _, snapshot := range linstorSnapshots {
		snapshots = append(snapshots, snapshotMap[snapshot.Name])
	}

	return snapshots, nil
}

// BackupVolume copies a volume (and optionally its snapshots) to a specified target path.
// This driver does not support optimized backups.
func (d *linstor) BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error {
	return genericVFSBackupVolume(d, vol, tarWriter, snapshots, op)
}

// CreateVolumeFromBackup restores a backup tarball onto the storage device.
func (d *linstor) CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error) {
	return genericVFSBackupUnpack(d, d.state.OS, vol, srcBackup.Snapshots, srcData, op)
}
