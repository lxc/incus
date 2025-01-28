package drivers

import (
	"context"
	"fmt"

	linstorClient "github.com/LINBIT/golinstor/client"

	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/shared/logger"
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

	// TODO: run filler function if supplied

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
