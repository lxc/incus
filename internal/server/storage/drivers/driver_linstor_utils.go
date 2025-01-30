package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	linstorClient "github.com/LINBIT/golinstor/client"

	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// LinstorDefaultResourceGroupPlaceCount represents the default Linstor resource group place count.
const LinstorDefaultResourceGroupPlaceCount = "2"

// LinstorDefaultVolumePrefix represents the default Linstor volume prefix.
const LinstorDefaultVolumePrefix = "incus-volume-"

// LinstorResourceGroupNameConfigKey represents the config key that describes the resource group name.
const LinstorResourceGroupNameConfigKey = "linstor.resource_group.name"

// LinstorResourceGroupPlaceCountConfigKey represents the config key that describes the resource group place count.
const LinstorResourceGroupPlaceCountConfigKey = "linstor.resource_group.place_count"

// LinstorResourceGroupStoragePoolConfigKey represents the config key that describes the resource group storage pool.
const LinstorResourceGroupStoragePoolConfigKey = "linstor.resource_group.storage_pool"

// LinstorVolumePrefixConfigKey represents the config key that describes the prefix to add to every volume within a storage pool.
const LinstorVolumePrefixConfigKey = "linstor.volume.prefix"

// drbdVersion returns the DRBD version of the currently loaded kernel module.
func (d *linstor) drbdVersion() (*version.DottedVersion, error) {
	modulePath := "/sys/module/drbd/version"

	if !util.PathExists(modulePath) {
		return nil, fmt.Errorf("Could not determine DRBD module version: Module not loaded")
	}

	rawVersion, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("Could not determine DRBD module version: %w", err)
	}

	ver, err := version.Parse(strings.TrimSpace(string(rawVersion)))
	if err != nil {
		return nil, fmt.Errorf("Could not determine DRBD module version: %w", err)
	}

	return ver, nil
}

// resourceGroupExists returns whether the resource group associated with the current storage pool exists.
func (d *linstor) resourceGroupExists() (bool, error) {
	resourceGroup, err := d.getResourceGroup()
	if err != nil {
		return false, fmt.Errorf("Could not get resource group: %w", err)
	}

	if resourceGroup == nil {
		return false, nil
	}

	return true, nil
}

// getResourceGroup fetches the resource group for the storage pool.
func (d *linstor) getResourceGroup() (*linstorClient.ResourceGroup, error) {
	d.logger.Debug("Fetching Linstor resource group")

	// Retrieve the Linstor client
	linstor, err := d.state.Linstor()
	if err != nil {
		return nil, err
	}

	resourceGroupName := d.config[LinstorResourceGroupNameConfigKey]
	resourceGroup, err := linstor.Client.ResourceGroups.Get(context.TODO(), resourceGroupName)
	if errors.Is(err, linstorClient.NotFoundError) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Could not get Linstor resource group: %w", err)
	}

	return &resourceGroup, nil
}

// createResourceGroup creates a new resource group for the storage pool.
func (d *linstor) createResourceGroup() error {
	d.logger.Debug("Creating Linstor resource group")

	// Retrieve the Linstor client
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	placeCount, err := strconv.Atoi(d.config[LinstorResourceGroupPlaceCountConfigKey])
	if err != nil {
		return fmt.Errorf("Could not parse resource group place count property: %w", err)
	}

	resourceGroup := linstorClient.ResourceGroup{
		Name:        d.config[LinstorResourceGroupNameConfigKey],
		Description: "Resource group managed by Incus",
		SelectFilter: linstorClient.AutoSelectFilter{
			PlaceCount: int32(placeCount),
		},
	}

	if d.config[LinstorResourceGroupStoragePoolConfigKey] != "" {
		resourceGroup.SelectFilter.StoragePool = d.config[LinstorResourceGroupStoragePoolConfigKey]
	}

	err = linstor.Client.ResourceGroups.Create(context.TODO(), resourceGroup)
	if err != nil {
		return fmt.Errorf("Could not create Linstor resource group : %w", err)
	}

	return nil
}

// deleteResourceGroup deleter the resource group for the storage pool.
func (d *linstor) deleteResourceGroup() error {
	// Retrieve the Linstor client
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	err = linstor.Client.ResourceGroups.Delete(context.TODO(), d.config[LinstorResourceGroupNameConfigKey])
	if err != nil {
		return fmt.Errorf("Could not delete Linstor resource group : %w", err)
	}

	return nil
}

// getResourceDefinition returns a Resource Definition instance for the given Resource name.
func (d *linstor) getResourceDefinition(resourceDefinitionName string) (*linstorClient.ResourceDefinition, error) {
	linstor, err := d.state.Linstor()
	if err != nil {
		return nil, err
	}

	resourceDefinition, err := linstor.Client.ResourceDefinitions.Get(context.TODO(), resourceDefinitionName)
	if errors.Is(err, linstorClient.NotFoundError) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Could not find the resource definition: %w", err)
	}

	return &resourceDefinition, nil
}

// getResourceDefinitionName returns the Linstor resource definition name for a given `vol`.
func (d *linstor) getResourceDefinitionName(vol Volume) (string, error) {
	id, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return "", fmt.Errorf("Failed to get volume ID for %s: %w", vol.name, err)
	}

	return d.config[LinstorVolumePrefixConfigKey] + strconv.FormatInt(id, 10), nil
}

// getLinstorDevPath return the device path for a given `vol`.
func (d *linstor) getLinstorDevPath(vol Volume) (string, error) {
	linstor, err := d.state.Linstor()
	if err != nil {
		return "", err
	}

	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return "", err
	}

	// Fetching all the nodes that contains the volume definition
	nodes, err := linstor.Client.Nodes.GetAll(context.TODO(), &linstorClient.ListOpts{
		Resource: []string{resourceDefinitionName},
	})
	if err != nil {
		return "", fmt.Errorf("Unable to get the nodes for the resource definition: %w", err)
	}

	volumeIndex := 0

	// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource
	if vol.volType == VolumeTypeVM && vol.contentType == ContentTypeFS {
		volumeIndex = 1
	}

	volume, err := linstor.Client.Resources.GetVolume(context.TODO(), resourceDefinitionName, nodes[0].Name, volumeIndex)
	if err != nil {
		return "", fmt.Errorf("Unable to get Linstor volume: %w", err)
	}

	// TODO: maybe we should get the device path from /dev/drbd/by-res/<name>/0 like Cinder does
	// https://github.com/LINBIT/openstack-cinder/blob/linstor/master/cinder/volume/drivers/linstordrv.py#L1065
	return volume.DevicePath, nil
}

// getSatelliteName returns the local LINSTOR satellite name.
func (d *linstor) getSatelliteName() string {
	name := d.state.LocalConfig.LinstorSatelliteName()
	if name != "" {
		return name
	}

	if d.state.ServerClustered {
		return d.state.ServerName
	}

	return d.state.OS.Hostname
}

// makeVolumeAvailable makes a volume available on the current node.
func (d *linstor) makeVolumeAvailable(vol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Making volume available on node")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.MakeAvailable(context.TODO(), resourceName, d.getSatelliteName(), linstorClient.ResourceMakeAvailable{
		Diskful: false,
	})
	if err != nil {
		l.Debug("Could not make resource available on node", logger.Ctx{"resourceName": resourceName, "nodeName": d.getSatelliteName(), "error": err})
		return fmt.Errorf("Could not make resource available on node: %w", err)
	}

	return nil
}
