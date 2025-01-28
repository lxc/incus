package drivers

import (
	"context"
	"fmt"

	linstorClient "github.com/LINBIT/golinstor/client"

	"github.com/lxc/incus/v6/internal/server/operations"
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
	d.logger.Debug("Creating a new Linstor volume")

	linstor, err := d.state.Linstor()
	if err != nil {
		return fmt.Errorf("Unable to get the Linstor client: %w", err)
	}

	// Transform byte to KiB.
	requiredKiB, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return fmt.Errorf("Unable to parse volume size: %w", err)
	}
	requiredKiB = requiredKiB / 1024

	resourceDefinitionName := d.getResourceDefinitionName(vol.Name())

	err = linstor.Client.ResourceGroups.Spawn(context.TODO(), d.config[LinstorResourceGroupNameConfigKey], linstorClient.ResourceGroupSpawn{
		ResourceDefinitionName: resourceDefinitionName,
		VolumeSizes:            []int64{requiredKiB},
	})
	if err != nil {
		return fmt.Errorf("Unable to spawn from resource group: %w", err)
	}

	// TODO: run filler function if supplied

	return nil
}

// DeleteVolume deletes a volume of the storage device.
func (d *linstor) DeleteVolume(vol Volume, op *operations.Operation) error {
	d.logger.Debug("Deleting Linstor volume")

	linstor, err := d.state.Linstor()
	if err != nil {
		return fmt.Errorf("Unable to get the Linstor client: %w", err)
	}

	// Test if the volume exists
	volumeExists, err := d.HasVolume(vol)
	if err != nil {
		return fmt.Errorf("Unable to check if volume exists: %w", err)
	}

	if !volumeExists {
		d.logger.Warn("Resource definition does not exist")
	} else {
		err = linstor.Client.ResourceDefinitions.Delete(context.TODO(), d.getResourceDefinitionName(vol.Name()))
		if err != nil {
			return fmt.Errorf("Unable to delete the resource definition: %w", err)
		}
	}

	return nil
}

// HasVolume indicates whether a specific volume exists on the storage pool.
func (d *linstor) HasVolume(vol Volume) (bool, error) {
	resourceDefinitionName := d.getResourceDefinitionName(vol.Name())
	resourceDefinition, err := d.getResourceDefinition(resourceDefinitionName)
	if err != nil {
		return false, err
	}

	if resourceDefinition == nil {
		return false, nil
	}
	return true, nil
}
