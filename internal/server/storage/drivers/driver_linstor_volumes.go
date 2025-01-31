package drivers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	linstorClient "github.com/LINBIT/golinstor/client"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/operations"
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
	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

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

	// Setup the filesystem.
	err = d.makeVolumeAvailable(vol)
	if err != nil {
		return fmt.Errorf("Could not make volume available for filesystem creation: %w", err)
	}

	devPath, err := d.getLinstorDevPath(vol)
	if err != nil {
		return fmt.Errorf("Could not get device path for filesystem creation: %w", err)
	}

	volFilesystem := vol.ConfigBlockFilesystem()
	if vol.contentType == ContentTypeFS {
		_, err = makeFSType(devPath, volFilesystem, nil)
		if err != nil {
			return err
		}
	}

	// For VMs, also create the filesystem on the associated filesystem volume.
	if vol.IsVMBlock() {
		fsVol := vol.NewVMBlockFilesystemVolume()
		fsVolDevPath, err := d.getLinstorDevPath(fsVol)
		if err != nil {
			return fmt.Errorf("Could not get device path for filesystem creation: %w", err)
		}

		fsVolFilesystem := fsVol.ConfigBlockFilesystem()
		_, err = makeFSType(fsVolDevPath, fsVolFilesystem, nil)
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
		name, err := d.getResourceDefinitionName(vol)
		if err != nil {
			return err
		}

		err = linstor.Client.ResourceDefinitions.Delete(context.TODO(), name)
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
	// If we cannot find the definition name, we can conclude that the volume does not exist.
	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return false, nil
	}

	resourceDefinition, err := d.getResourceDefinition(resourceDefinitionName)
	if err != nil {
		return false, err
	}

	if resourceDefinition == nil {
		return false, nil
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

	err = d.makeVolumeAvailable(vol)
	if err != nil {
		return fmt.Errorf("Could not mount volume: %w", err)
	}

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

	// At this point the volume name is already updated on the database, so we
	// clone it and set the new name to be able to fetch its ID from the database.
	newVol := vol.Clone()
	newVol.name = newVolName

	resourceDefinitionName, err := d.getResourceDefinitionName(newVol)
	if err != nil {
		return err
	}

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinitionName, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{
			"Aux/Incus/name": newVolName,
		},
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	return nil
}
