package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	linstorClient "github.com/LINBIT/golinstor/client"
	"github.com/LINBIT/golinstor/clonestatus"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
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

// errResourceDefinitionNotFound indicates that a resource definition could not be found in Linstor.
var errResourceDefinitionNotFound = errors.New("Resource definition not found")

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

// getResourceGroupSize fetches the resource group size info.
func (d *linstor) getResourceGroupSize() (*linstorClient.QuerySizeInfoResponseSpaceInfo, error) {
	// Retrieve the Linstor client.
	linstor, err := d.state.Linstor()
	if err != nil {
		return nil, err
	}

	placeCount, err := strconv.Atoi(d.config[LinstorResourceGroupPlaceCountConfigKey])
	if err != nil {
		return nil, fmt.Errorf("Could not parse resource group place count property: %w", err)
	}

	resourceGroupName := d.config[LinstorResourceGroupNameConfigKey]
	request := linstorClient.QuerySizeInfoRequest{
		SelectFilter: &linstorClient.AutoSelectFilter{
			PlaceCount: int32(placeCount),
		},
	}

	if d.config[LinstorResourceGroupStoragePoolConfigKey] != "" {
		request.SelectFilter.StoragePool = d.config[LinstorResourceGroupStoragePoolConfigKey]
	}

	response, err := linstor.Client.ResourceGroups.QuerySizeInfo(context.TODO(), resourceGroupName, request)
	if err != nil {
		return nil, err
	}

	return response.SpaceInfo, nil
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

// updateResourceGroup updates the resource group for the storage pool.
func (d *linstor) updateResourceGroup(changedConfig map[string]string) error {
	d.logger.Debug("Updating Linstor resource group")

	// Retrieve the Linstor client.
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceGroupModify := linstorClient.ResourceGroupModify{}

	placeCount, changed := changedConfig[LinstorResourceGroupPlaceCountConfigKey]
	if changed {
		placeCount, err := strconv.Atoi(placeCount)
		if err != nil {
			return fmt.Errorf("Could not parse resource group place count property: %w", err)
		}

		resourceGroupModify.SelectFilter.PlaceCount = int32(placeCount)
	}

	storagePool, changed := changedConfig[LinstorResourceGroupStoragePoolConfigKey]
	if changed {
		resourceGroupModify.SelectFilter.StoragePool = storagePool
	}

	resourceGroupName := d.config[LinstorResourceGroupNameConfigKey]

	err = linstor.Client.ResourceGroups.Modify(context.TODO(), resourceGroupName, resourceGroupModify)
	if err != nil {
		return fmt.Errorf("Could not update Linstor resource group : %w", err)
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

	// Fetching all the nodes that contains the volume definition.
	nodes, err := linstor.Client.Nodes.GetAll(context.TODO(), &linstorClient.ListOpts{
		Resource: []string{resourceDefinitionName},
	})
	if err != nil {
		return "", fmt.Errorf("Unable to get the nodes for the resource definition: %w", err)
	}

	volumes, err := linstor.Client.Resources.GetVolumes(context.TODO(), resourceDefinitionName, nodes[0].Name)
	if err != nil {
		return "", fmt.Errorf("Unable to get Linstor volumes: %w", err)
	}

	volumeIndex := 0
	if len(volumes) == 2 {
		// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
		if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeFS {
			volumeIndex = 1
		}
	}

	return volumes[volumeIndex].DevicePath, nil
}

// getSatelliteName returns the local LINSTOR satellite name.
//
// The logic used to determine the satellite name is documented in the public
// driver documentation, as it is relevant for users. Therefore, any changes
// to the external behavior of this function should also be reflected in the
// public documentation.
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

// createVolumeSnapshot creates a volume snapshot.
func (d *linstor) createVolumeSnapshot(parentVol Volume, snapVol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Creating volume snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceName, err := d.getResourceDefinitionName(parentVol)
	if err != nil {
		return err
	}

	snapResourceName, err := d.getResourceDefinitionName(snapVol)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.CreateSnapshot(context.TODO(), linstorClient.Snapshot{
		Name:         snapResourceName,
		ResourceName: resourceName,
	})
	if err != nil {
		return fmt.Errorf("Could not create resource snapshot: %w", err)
	}

	return nil
}

// deleteVolumeSnapshot deletes a volume snapshot.
func (d *linstor) deleteVolumeSnapshot(parentVol Volume, snapVol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Deleting volume snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceName, err := d.getResourceDefinitionName(parentVol)
	if err != nil {
		return err
	}

	snapResourceName, err := d.getResourceDefinitionName(snapVol)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.DeleteSnapshot(context.TODO(), resourceName, snapResourceName)
	if err != nil {
		return fmt.Errorf("Could not delete resource snapshot: %w", err)
	}

	return nil
}

// snapshotExists returns whether the given snapshot exists.
func (d *linstor) snapshotExists(parentVol Volume, snapVol Volume) (bool, error) {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Fetching Linstor snapshot")

	// Retrieve the Linstor client.
	linstor, err := d.state.Linstor()
	if err != nil {
		return false, err
	}

	resourceName, err := d.getResourceDefinitionName(parentVol)
	if err != nil {
		return false, err
	}

	snapResourceName, err := d.getResourceDefinitionName(snapVol)
	if err != nil {
		return false, err
	}

	_, err = linstor.Client.Resources.GetSnapshot(context.TODO(), resourceName, snapResourceName)
	if errors.Is(err, linstorClient.NotFoundError) {
		l.Debug("Snapshot not found")
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("Could not get snapshot: %w", err)
	}

	l.Debug("Got snapshot")

	return true, nil
}

// restoreVolume restores a volume state from a snapshot.
func (d *linstor) restoreVolume(vol Volume, snapVol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"vol": vol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Restoring volume to snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

	snapResourceName, err := d.getResourceDefinitionName(snapVol)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.RollbackSnapshot(context.TODO(), resourceName, snapResourceName)
	if err != nil {
		return fmt.Errorf("Could not restore volume to snapshot: %w", err)
	}

	return nil
}

// createResourceFromSnapshot creates a new resource from a snapshot.
func (d *linstor) createResourceFromSnapshot(snapVol Volume, vol Volume) error {
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	rev := revert.New()
	defer rev.Fail()

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	parentResourceDefinitionName, err := d.getResourceDefinitionName(parentVol)
	if err != nil {
		return err
	}

	snapResourceDefinitionName, err := d.getResourceDefinitionName(snapVol)
	if err != nil {
		return err
	}

	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

	err = linstor.Client.ResourceDefinitions.Create(context.TODO(), linstorClient.ResourceDefinitionCreate{
		ResourceDefinition: linstorClient.ResourceDefinition{
			Name:              resourceDefinitionName,
			ResourceGroupName: d.config[LinstorResourceGroupNameConfigKey],
		},
	})
	if err != nil {
		return fmt.Errorf("Could not create resource definition from snapshot: %w", err)
	}

	rev.Add(func() { _ = linstor.Client.ResourceDefinitions.Delete(context.TODO(), resourceDefinitionName) })

	err = linstor.Client.Resources.RestoreVolumeDefinitionSnapshot(context.TODO(), parentResourceDefinitionName, snapResourceDefinitionName, linstorClient.SnapshotRestore{
		ToResource: resourceDefinitionName,
	})
	if err != nil {
		return fmt.Errorf("Could not restore volume definition from snapshot: %w", err)
	}

	err = linstor.Client.Resources.RestoreSnapshot(context.TODO(), parentResourceDefinitionName, snapResourceDefinitionName, linstorClient.SnapshotRestore{
		ToResource: resourceDefinitionName,
	})
	if err != nil {
		return fmt.Errorf("Could not restore resource from snapshot: %w", err)
	}

	// Set the aux properties on the new resource definition.
	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinitionName, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{
			"Aux/Incus/name":         vol.name,
			"Aux/Incus/type":         string(vol.volType),
			"Aux/Incus/content-type": string(vol.contentType),
		},
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	rev.Success()
	return nil
}

// resizeVolume resizes a volume definition. This function does not resize any filesystem inside the volume.
func (d *linstor) resizeVolume(vol Volume, sizeBytes int64) error {
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

	volumeIndex := 0

	// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
	if vol.volType == VolumeTypeVM && vol.contentType == ContentTypeFS {
		volumeIndex = 1
	}

	// Resize the volume definition.
	err = linstor.Client.ResourceDefinitions.ModifyVolumeDefinition(context.TODO(), resourceDefinitionName, volumeIndex, linstorClient.VolumeDefinitionModify{
		SizeKib: uint64(sizeBytes) / 1024,
	})
	if err != nil {
		return fmt.Errorf("Unable to resize volume definition: %w", err)
	}

	return nil
}

func (d *linstor) copyVolume(vol Volume, srcVol Volume) error {
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	rev := revert.New()
	defer rev.Fail()

	targetResourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return err
	}

	srcResourceDefinitionName, err := d.getResourceDefinitionName(srcVol)
	if err != nil {
		return err
	}

	_, err = linstor.Client.ResourceDefinitions.Clone(context.TODO(), srcResourceDefinitionName, linstorClient.ResourceDefinitionCloneRequest{
		Name: targetResourceDefinitionName,
	})
	if err != nil {
		return fmt.Errorf("Unable to start cloning resource definition: %w", err)
	}

	d.logger.Debug("Clone operation started. Will poll for status", logger.Ctx{"srcResourceDefinition": srcResourceDefinitionName, "targetResourceDefinition": targetResourceDefinitionName})

	// Poll the cloning operation status from LINSTOR. The duration of the operation depends on the
	// underlying storage backend being used. For LVM-thin and ZFS the cloning is optimized and should
	// be considerably faster than for LVM, which uses `dd`.
loop:
	for {
		cloneStatus, err := linstor.Client.ResourceDefinitions.CloneStatus(context.TODO(), srcResourceDefinitionName, targetResourceDefinitionName)
		if err != nil {
			return fmt.Errorf("Unable to get clone operation status: %w", err)
		}

		d.logger.Debug("Got resource definition clone status", logger.Ctx{"cloneStatus": cloneStatus.Status})

		switch cloneStatus.Status {
		case clonestatus.Complete:
			break loop
		case clonestatus.Cloning:
			time.Sleep(1 * time.Second)
		case clonestatus.Failed:
			return fmt.Errorf("Clone operation failed")
		}
	}

	rev.Add(func() { _ = linstor.Client.ResourceDefinitions.Delete(context.TODO(), targetResourceDefinitionName) })

	// Set the aux properties on the new resource definition.
	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), targetResourceDefinitionName, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{
			"Aux/Incus/name":         vol.name,
			"Aux/Incus/type":         string(vol.volType),
			"Aux/Incus/content-type": string(vol.contentType),
		},
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	rev.Success()

	return nil
}

func (d *linstor) getVolumeSize(vol Volume) (int64, error) {
	linstor, err := d.state.Linstor()
	if err != nil {
		return 0, err
	}

	resourceDefinitionName, err := d.getResourceDefinitionName(vol)
	if err != nil {
		return 0, err
	}

	// Get the volume definitions.
	volumeDefinitions, err := linstor.Client.ResourceDefinitions.GetVolumeDefinitions(context.TODO(), resourceDefinitionName)
	if err != nil {
		return 0, fmt.Errorf("Unable to get volume definition: %w", err)
	}

	volumeIndex := 0
	if len(volumeDefinitions) == 2 {
		// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
		if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeFS {
			volumeIndex = 1
		}
	}

	return int64(volumeDefinitions[volumeIndex].SizeKib * 1024), nil
}
