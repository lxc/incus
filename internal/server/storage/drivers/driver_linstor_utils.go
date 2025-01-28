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
	"github.com/lxc/incus/v6/shared/util"
)

// LinstorDefaultResourceGroupName represents the default Linstor resource group name.
const LinstorDefaultResourceGroupName = "incus"

// LinstorDefaultResourceGroupPlaceCount represents the default Linstor resource group place count.
const LinstorDefaultResourceGroupPlaceCount = "2"

// LinstorResourceGroupNameConfigKey represents the config key that describes the resource group name.
const LinstorResourceGroupNameConfigKey = "linstor.resource_group.name"

// LinstorResourceGroupPlaceCountConfigKey represents the config key that describes the resource group place count.
const LinstorResourceGroupPlaceCountConfigKey = "linstor.resource_group.place_count"

// LinstorResourceGroupStoragePoolConfigKey represents the config key that describes the resource group storage pool.
const LinstorResourceGroupStoragePoolConfigKey = "linstor.resource_group.storage_pool"

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
		return nil, fmt.Errorf("Could not load Linstor client: %w", err)
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
		return fmt.Errorf("Could not load Linstor client: %w", err)
	}

	placeCount, err := strconv.Atoi(d.config[LinstorResourceGroupPlaceCountConfigKey])
	if err != nil {
		return fmt.Errorf("Could not parse resource group place count property: %w", err)
	}

	resourceGroup := linstorClient.ResourceGroup{
		Name:        d.config[LinstorResourceGroupNameConfigKey],
		Description: "Resource group managed by Incus to provide volumes",
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
		return fmt.Errorf("Could not load Linstor client: %w", err)
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
		return nil, fmt.Errorf("Could not load Linstor client: %w", err)
	}

	resourceDefinition, err := linstor.Client.ResourceDefinitions.Get(context.TODO(), resourceDefinitionName)
	if errors.Is(err, linstorClient.NotFoundError) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Could not find the resource definition: %w", err)
	}

	return &resourceDefinition, nil
}

// getResourceDefinitionName returns the Linstor resource definition name for a given `volName`.
func (d *linstor) getResourceDefinitionName(volName string) string {
	// TODO: set an unique ID on the name so we can later rename the resource at the
	// Incus level, since Linstor does not support renaming
	return fmt.Sprintf("%s-%s", d.config[LinstorResourceGroupNameConfigKey], volName)
}
