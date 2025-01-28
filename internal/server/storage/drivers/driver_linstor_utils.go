package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	linstorClient "github.com/LINBIT/golinstor/client"

	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// LinstorSatellitePaths lists the possible FS paths for the Satellite script.
var LinstorSatellitePaths = []string{"/usr/share/linstor-server/bin"}

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
func (d *linstor) drbdVersion() (string, error) {
	modulePath := "/sys/module/drbd/version"

	if !util.PathExists(modulePath) {
		return "", fmt.Errorf("Could not determine DRBD module version: Module not loaded")
	}

	ver, err := os.ReadFile(modulePath)
	if err != nil {
		return "", fmt.Errorf("Could not determine DRBD module version: %w", err)
	}

	return strings.TrimSpace(string(ver)), nil
}

// controllerVersion returns the LINSTOR controller version.
func (d *linstor) controllerVersion() (string, error) {
	var satellitePath string
	for _, path := range LinstorSatellitePaths {
		candidate := filepath.Join(path, "Satellite")
		_, err := os.Stat(candidate)
		if err == nil {
			satellitePath = candidate
			break
		}
	}

	if satellitePath == "" {
		return "", errors.New("LINSTOR satellite executable not found")
	}

	out, err := subprocess.RunCommand(satellitePath, "--version")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Version:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return "", errors.New("Could not parse LINSTOR satellite version")
			}

			return fields[1], nil
		}
	}

	return "", errors.New("Could not parse LINSTOR satellite version")
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

	// Retrieve the Linstor client.
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

	// Retrieve the Linstor client.
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
	// Retrieve the Linstor client.
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
