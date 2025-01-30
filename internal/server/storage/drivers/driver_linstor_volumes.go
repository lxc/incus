package drivers

import (
	"context"
	"fmt"

	linstorClient "github.com/LINBIT/golinstor/client"

	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/units"
)

// FillVolumeConfig populate volume with default config.
func (d *linstor) FillVolumeConfig(vol Volume) error {
	return nil
}

// ValidateVolume validates the supplied volume config.
func (d *linstor) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return nil
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
