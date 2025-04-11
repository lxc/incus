package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	linstorClient "github.com/LINBIT/golinstor/client"
	"github.com/LINBIT/golinstor/clonestatus"
	"github.com/google/uuid"

	"github.com/lxc/incus/v6/internal/migration"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
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

// DrbdOnNoQuorumConfigKey represents the config key that describes the DRBD policy when quorum is not reached.
const DrbdOnNoQuorumConfigKey = "drbd.on_no_quorum"

// DrbdAutoDiskfulConfigKey represents the config key that describes the DRBD timeout for toggling a diskful resource.
const DrbdAutoDiskfulConfigKey = "drbd.auto_diskful"

// DrbdAutoAddQuorumTiebreakerConfigKey represents the config key that describes whether DRBD will automatically create tiebreaker resources.
const DrbdAutoAddQuorumTiebreakerConfigKey = "drbd.auto_add_quorum_tiebreaker"

// LinstorRemoveSnapshotsConfigKey represents the config key that describes whether snapshots should be automatically removed with volumes.
const LinstorRemoveSnapshotsConfigKey = "linstor.remove_snapshots"

// LinstorAuxSnapshotPrefix represents the AuxProp prefix to map Incus and LINSTOR snapshots.
const LinstorAuxSnapshotPrefix = "Aux/Incus/snapshot-name/"

// LinstorAuxName represents the AuxProp storing the Incus volume name.
const LinstorAuxName = "Aux/Incus/name"

// LinstorAuxType represents the AuxProp storing the Incus volume type.
const LinstorAuxType = "Aux/Incus/type"

// LinstorAuxContentType represents the AuxProp storing the Incus volume content type.
const LinstorAuxContentType = "Aux/Incus/content-type"

// errResourceDefinitionNotFound indicates that a resource definition could not be found in Linstor.
var errResourceDefinitionNotFound = errors.New("Resource definition not found")

// errSnapshotNotFound indicates that a snapshot could not be found in Linstor.
var errSnapshotNotFound = errors.New("Resource definition not found")

// drbdPropsMap maps incus config keys to DRBD options.
var drbdPropsMap = map[string]string{
	DrbdOnNoQuorumConfigKey:              "DrbdOptions/Resource/on-no-quorum",
	DrbdAutoDiskfulConfigKey:             "DrbdOptions/auto-diskful",
	DrbdAutoAddQuorumTiebreakerConfigKey: "DrbdOptions/auto-add-quorum-tiebreaker",
}

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
func (d *linstor) getResourceGroupSize() (int64, int64, error) {
	// Retrieve the Linstor client.
	linstor, err := d.state.Linstor()
	if err != nil {
		return 0, 0, err
	}

	resourceGroupName := d.config[LinstorResourceGroupNameConfigKey]
	resourceGroup, err := linstor.Client.ResourceGroups.Get(context.TODO(), resourceGroupName)
	if err != nil {
		return 0, 0, fmt.Errorf("Could not get Linstor resource group: %w", err)
	}

	storagePools, err := linstor.Client.Nodes.GetStoragePoolView(context.TODO(), &linstorClient.ListOpts{
		StoragePool: resourceGroup.SelectFilter.StoragePoolList,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("Could not get Linstor storage pools: %w", err)
	}

	freeCapacity := int64(0)
	totalCapacity := int64(0)
	for _, storagePool := range storagePools {
		freeCapacity += storagePool.FreeCapacity
		totalCapacity += storagePool.TotalCapacity
	}

	return freeCapacity, totalCapacity, nil
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

	// Create the resource group.
	err = linstor.Client.ResourceGroups.Create(context.TODO(), resourceGroup)
	if err != nil {
		return fmt.Errorf("Could not create Linstor resource group : %w", err)
	}

	// Set additional properties based on the config.
	props, err := d.drbdPropsFromConfig(d.config)
	if err != nil {
		return fmt.Errorf("Could parse config into DRBD props: %w", err)
	}

	err = linstor.Client.ResourceGroups.Modify(context.TODO(), resourceGroup.Name, linstorClient.ResourceGroupModify{
		OverrideProps: props,
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on Linstor resource group : %w", err)
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

	// Parse and set properties to be overwritten.
	overrideProps, err := d.drbdPropsFromConfig(changedConfig)
	if err != nil {
		return fmt.Errorf("Could parse config into DRBD props: %w", err)
	}

	resourceGroupModify.OverrideProps = overrideProps

	// Parse and set properties to be deleted.
	deleteProps := []string{}
	for key, value := range changedConfig {
		if value != "" {
			continue
		}

		prop, ok := drbdPropsMap[key]
		if ok {
			deleteProps = append(deleteProps, prop)
		}
	}

	resourceGroupModify.DeleteProps = deleteProps

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

// getResourceDefinition returns the Linstor resource definition for a given volume.
func (d *linstor) getResourceDefinition(vol Volume, fetchVolumeDefinitions bool) (linstorClient.ResourceDefinitionWithVolumeDefinition, error) {
	l := logger.AddContext(logger.Ctx{"vol": vol.name, "volType": vol.volType, "contentType": vol.contentType})
	l.Debug("Getting resource definition for volume")
	linstor, err := d.state.Linstor()
	if err != nil {
		return linstorClient.ResourceDefinitionWithVolumeDefinition{}, err
	}

	// Query resource definitions that match the desired volume by its name.
	resourceDefinitions, err := linstor.Client.ResourceDefinitions.GetAll(context.TODO(), linstorClient.RDGetAllRequest{
		Props: []string{
			LinstorAuxName + "=" + d.config[LinstorVolumePrefixConfigKey] + vol.name,
			LinstorAuxType + "=" + string(vol.volType),
		},
		WithVolumeDefinitions: fetchVolumeDefinitions,
	})
	if err != nil {
		return linstorClient.ResourceDefinitionWithVolumeDefinition{}, err
	}

	l.Debug("Queried resource definitions", logger.Ctx{"query": LinstorAuxName + "=" + d.config[LinstorVolumePrefixConfigKey] + vol.name, "result": resourceDefinitions})

	// Filter resource definitions for the storage pool's resource group.
	var filteredResourceDefinitions []linstorClient.ResourceDefinitionWithVolumeDefinition
	for _, rd := range resourceDefinitions {
		if rd.ResourceGroupName == d.config[LinstorResourceGroupNameConfigKey] {
			filteredResourceDefinitions = append(filteredResourceDefinitions, rd)
		}
	}

	if len(filteredResourceDefinitions) == 0 {
		return linstorClient.ResourceDefinitionWithVolumeDefinition{}, errResourceDefinitionNotFound
	} else if len(filteredResourceDefinitions) > 1 {
		return linstorClient.ResourceDefinitionWithVolumeDefinition{}, fmt.Errorf("Multiple resource definitions found for volume %s", vol.name)
	}

	return filteredResourceDefinitions[0], nil
}

// getLinstorDevPath return the device path for a given `vol` in the current node.
//
// If the resource is not available on the current node, it is made available before
// fetching its device path.
func (d *linstor) getLinstorDevPath(vol Volume) (string, error) {
	l := logger.AddContext(logger.Ctx{"vol": vol.name, "volType": vol.volType, "contentType": vol.contentType})
	l.Debug("Getting device path")
	linstor, err := d.state.Linstor()
	if err != nil {
		return "", err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return "", err
	}

	// When retrieving the device path, always make sure that the resource is available on the current node.
	err = linstor.Client.Resources.MakeAvailable(context.TODO(), resourceDefinition.Name, d.getSatelliteName(), linstorClient.ResourceMakeAvailable{
		Diskful: false,
	})
	if err != nil {
		l.Debug("Could not make resource available on node", logger.Ctx{"resourceDefinitionName": resourceDefinition.Name, "nodeName": d.getSatelliteName(), "error": err})
		return "", fmt.Errorf("Could not make resource available on node: %w", err)
	}

	volumes, err := linstor.Client.Resources.GetVolumes(context.TODO(), resourceDefinition.Name, d.getSatelliteName())
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

// deleteDisklessResource deletes the diskless resource for the given volume in the current node if one exists.
func (d *linstor) deleteDisklessResource(vol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Checking for diskless resources to delete")

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	// Fetch all resources for the given volume in the current node.
	resources, err := linstor.Client.Resources.GetResourceView(context.TODO(), &linstorClient.ListOpts{
		Resource: []string{resourceDefinition.Name},
		Node:     []string{d.getSatelliteName()},
	})
	if err != nil {
		return fmt.Errorf("Unable to get the resources for the resource definition: %w", err)
	}

	l.Debug("Got resources for the volume in the current node", logger.Ctx{"resources": resources})

	// Delete the DISKLESS resources.
	for _, r := range resources {
		if slices.Contains(r.Flags, "DISKLESS") {
			l.Debug("Deleting diskless resource")
			err := linstor.Client.Resources.Delete(context.TODO(), r.Name, r.NodeName)
			if err != nil {
				return err
			}

			l.Debug("Deleted diskless resource")
		}
	}

	return nil
}

// getVolumeUsage returns the allocated size for a given volume in KiB.
func (d *linstor) getVolumeUsage(vol Volume) (int64, error) {
	l := d.logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Getting volume usage")

	linstor, err := d.state.Linstor()
	if err != nil {
		return 0, err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return 0, err
	}

	// Fetch all resources for the given volume.
	resources, err := linstor.Client.Resources.GetResourceView(context.TODO(), &linstorClient.ListOpts{
		Resource: []string{resourceDefinition.Name},
	})
	if err != nil {
		return 0, fmt.Errorf("Unable to get the resources for the resource definition: %w", err)
	}

	var resource *linstorClient.ResourceWithVolumes

	// Find the first non DISKLESS resource.
	for _, r := range resources {
		l.Debug("Volume flags", logger.Ctx{"node": r.NodeName, "flags": r.Flags})
		if !slices.Contains(r.Flags, "DISKLESS") {
			resource = &r
		}
	}

	// If no diskful resource is found, usage cannot be determined.
	if resource == nil {
		l.Warn("No diskful resource found for volume")
		return 0, nil
	}

	volumes := resource.Volumes

	volumeIndex := 0
	if len(volumes) == 2 {
		// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
		if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeFS {
			volumeIndex = 1
		}
	}

	return volumes[volumeIndex].AllocatedSizeKib, nil
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

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.MakeAvailable(context.TODO(), resourceDefinition.Name, d.getSatelliteName(), linstorClient.ResourceMakeAvailable{
		Diskful: false,
	})
	if err != nil {
		l.Debug("Could not make resource available on node", logger.Ctx{"resourceDefinitionName": resourceDefinition.Name, "nodeName": d.getSatelliteName(), "error": err})
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

	resourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		return err
	}

	linstorSnapshotName := d.generateUUIDWithPrefix()

	err = linstor.Client.Resources.CreateSnapshot(context.TODO(), linstorClient.Snapshot{
		Name:         linstorSnapshotName,
		ResourceName: resourceDefinition.Name,
	})
	if err != nil {
		return fmt.Errorf("Could not create resource snapshot: %w", err)
	}

	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.Name())
	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinition.Name, linstorClient.GenericPropsModify{
		OverrideProps: map[string]string{LinstorAuxSnapshotPrefix + snapshotName: linstorSnapshotName},
	})
	if err != nil {
		_ = linstor.Client.Resources.DeleteSnapshot(context.TODO(), resourceDefinition.Name, linstorSnapshotName)
		return err
	}

	return nil
}

// renameVolumeSnapshot renames a volume snapshot.
func (d *linstor) renameVolumeSnapshot(parentVol Volume, snapVol Volume, newSnapshotName string) error {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Renaming volume snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		return err
	}

	// Get the snapshot name.
	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.Name())
	linstorSnapshotName, ok := resourceDefinition.Props[LinstorAuxSnapshotPrefix+snapshotName]
	if !ok {
		return fmt.Errorf("Could not find snapshot name mapping for volume %s", parentVol.Name())
	}

	return linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinition.Name, linstorClient.GenericPropsModify{
		DeleteProps:   linstorClient.DeleteProps{LinstorAuxSnapshotPrefix + snapshotName},
		OverrideProps: map[string]string{LinstorAuxSnapshotPrefix + newSnapshotName: linstorSnapshotName},
	})
}

// deleteVolumeSnapshot deletes a volume snapshot.
func (d *linstor) deleteVolumeSnapshot(parentVol Volume, snapVol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Deleting volume snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		return err
	}

	// Get the snapshot name.
	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.Name())
	linstorSnapshotName, ok := resourceDefinition.Props[LinstorAuxSnapshotPrefix+snapshotName]
	if !ok {
		return fmt.Errorf("Could not find snapshot name mapping for volume %s", parentVol.Name())
	}

	err = linstor.Client.Resources.DeleteSnapshot(context.TODO(), resourceDefinition.Name, linstorSnapshotName)
	if err != nil {
		return fmt.Errorf("Could not delete resource snapshot: %w", err)
	}

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinition.Name, linstorClient.GenericPropsModify{
		DeleteProps: []string{LinstorAuxSnapshotPrefix + snapshotName},
	})
	if err != nil {
		return fmt.Errorf("Could not delete snapshot name mapping aux property: %w", err)
	}

	return nil
}

// getSnapshotMap gets the map from LINSTOR snapshot names to Incus’.
func (d *linstor) getSnapshotMap(parentVol Volume) (map[string]string, error) {
	l := d.logger.AddContext(logger.Ctx{"parentVol": parentVol.Name()})
	l.Debug("Getting snapshot map")
	result := make(map[string]string)

	resourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		return result, err
	}

	for key, value := range resourceDefinition.Props {
		if strings.HasPrefix(key, LinstorAuxSnapshotPrefix) {
			result[value] = strings.TrimPrefix(key, LinstorAuxSnapshotPrefix)
		}
	}

	return result, nil
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

	resourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		if errors.Is(err, errResourceDefinitionNotFound) {
			return false, nil
		}

		return false, err
	}

	linstorSnapshotName, err := d.getLinstorSnapshotName(snapVol, resourceDefinition)
	if err != nil {
		if errors.Is(err, errSnapshotNotFound) {
			return false, nil
		}

		return false, err
	}

	_, err = linstor.Client.Resources.GetSnapshot(context.TODO(), resourceDefinition.Name, linstorSnapshotName)
	if errors.Is(err, linstorClient.NotFoundError) {
		l.Debug("Snapshot not found")
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("Could not get snapshot: %w", err)
	}

	l.Debug("Got snapshot")

	return true, nil
}

// getLinstorSnapshotName returns the Linstor snapshot name given a snapshot and its parent resource definition.
func (d *linstor) getLinstorSnapshotName(snapVol Volume, resourceDefinition linstorClient.ResourceDefinitionWithVolumeDefinition) (string, error) {
	_, snapshotName, _ := api.GetParentAndSnapshotName(snapVol.Name())
	linstorSnapshotName, ok := resourceDefinition.Props[LinstorAuxSnapshotPrefix+snapshotName]
	if !ok {
		return "", errSnapshotNotFound
	}

	return linstorSnapshotName, nil
}

// restoreVolume restores a volume state from a snapshot.
func (d *linstor) restoreVolume(vol Volume, snapVol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"vol": vol.Name(), "snapVol": snapVol.Name()})
	l.Debug("Restoring volume to snapshot")
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	linstorSnapshotName, err := d.getLinstorSnapshotName(snapVol, resourceDefinition)
	if err != nil {
		return err
	}

	err = linstor.Client.Resources.RollbackSnapshot(context.TODO(), resourceDefinition.Name, linstorSnapshotName)
	if err != nil {
		return fmt.Errorf("Could not restore volume to snapshot: %w", err)
	}

	return nil
}

// createResourceDefinitionFromSnapshot creates a new resource definition from a snapshot.
func (d *linstor) createResourceDefinitionFromSnapshot(snapVol Volume, vol Volume) error {
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	rev := revert.New()
	defer rev.Fail()

	parentName, _, _ := api.GetParentAndSnapshotName(snapVol.name)
	parentVol := NewVolume(d, d.name, snapVol.volType, snapVol.contentType, parentName, nil, nil)

	parentResourceDefinition, err := d.getResourceDefinition(parentVol, false)
	if err != nil {
		return err
	}

	linstorSnapshotName, err := d.getLinstorSnapshotName(snapVol, parentResourceDefinition)
	if err != nil {
		return err
	}

	resourceDefinitionName := d.generateUUIDWithPrefix()

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

	err = linstor.Client.Resources.RestoreVolumeDefinitionSnapshot(context.TODO(), parentResourceDefinition.Name, linstorSnapshotName, linstorClient.SnapshotRestore{
		ToResource: resourceDefinitionName,
	})
	if err != nil {
		return fmt.Errorf("Could not restore volume definition from snapshot: %w", err)
	}

	err = linstor.Client.Resources.RestoreSnapshot(context.TODO(), parentResourceDefinition.Name, linstorSnapshotName, linstorClient.SnapshotRestore{
		ToResource: resourceDefinitionName,
	})
	if err != nil {
		return fmt.Errorf("Could not restore resource from snapshot: %w", err)
	}

	// Set the aux properties on the new resource definition.
	err = d.setResourceDefinitionProperties(vol, resourceDefinitionName)
	if err != nil {
		return err
	}

	rev.Success()
	return nil
}

// deleteResourceDefinitionFromSnapshot deletes the resource definition created from a snapshot.
func (d *linstor) deleteResourceDefinitionFromSnapshot(vol Volume) error {
	l := d.logger.AddContext(logger.Ctx{"vol": vol.Name()})
	l.Debug("Deleting resource definition for snapshot")

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		if errors.Is(err, errResourceDefinitionNotFound) {
			return nil
		}

		return err
	}

	err = linstor.Client.ResourceDefinitions.Delete(context.TODO(), resourceDefinition.Name)
	if err != nil {
		return err
	}

	d.logger.Debug("Resource definition for snapshot deleted")

	return nil
}

// resizeVolume resizes a volume definition. This function does not resize any filesystem inside the volume.
func (d *linstor) resizeVolume(vol Volume, sizeBytes int64) error {
	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	volumeIndex := 0

	// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
	if vol.volType == VolumeTypeVM && vol.contentType == ContentTypeFS {
		volumeIndex = 1
	}

	// Resize the volume definition.
	err = linstor.Client.ResourceDefinitions.ModifyVolumeDefinition(context.TODO(), resourceDefinition.Name, volumeIndex, linstorClient.VolumeDefinitionModify{
		SizeKib: uint64(sizeBytes) / 1024,
	})
	if err != nil {
		return fmt.Errorf("Unable to resize volume definition: %w", err)
	}

	return nil
}

func (d *linstor) copyVolume(vol Volume, srcVol Volume) error {
	l := logger.AddContext(logger.Ctx{"vol": vol.name, "srcVol": srcVol.name})
	l.Debug("Copying volume")

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	rev := revert.New()
	defer rev.Fail()

	targetResourceDefinitionName := d.generateUUIDWithPrefix()

	srcResourceDefinition, err := d.getResourceDefinition(srcVol, false)
	if err != nil {
		return err
	}

	_, err = linstor.Client.ResourceDefinitions.Clone(context.TODO(), srcResourceDefinition.Name, linstorClient.ResourceDefinitionCloneRequest{
		Name:          targetResourceDefinitionName,
		ResourceGroup: d.config[LinstorResourceGroupNameConfigKey],
	})
	if err != nil {
		return fmt.Errorf("Unable to start cloning resource definition: %w", err)
	}

	d.logger.Debug("Clone operation started. Will poll for status", logger.Ctx{"srcResourceDefinition": srcResourceDefinition.Name, "targetResourceDefinition": targetResourceDefinitionName})

	// Poll the cloning operation status from LINSTOR. The duration of the operation depends on the
	// underlying storage backend being used. For LVM-thin and ZFS the cloning is optimized and should
	// be considerably faster than for LVM, which uses `dd`.
loop:
	for {
		cloneStatus, err := linstor.Client.ResourceDefinitions.CloneStatus(context.TODO(), srcResourceDefinition.Name, targetResourceDefinitionName)
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
	err = d.setResourceDefinitionProperties(vol, targetResourceDefinitionName)
	if err != nil {
		return err
	}

	rev.Success()

	return nil
}

func (d *linstor) getVolumeSize(vol Volume) (int64, error) {
	resourceDefinition, err := d.getResourceDefinition(vol, true)
	if err != nil {
		return 0, err
	}

	volumeIndex := 0
	if len(resourceDefinition.VolumeDefinitions) == 2 {
		// For VM volumes, the associated filesystem volume is a second volume on the same LINSTOR resource.
		if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeFS {
			volumeIndex = 1
		}
	}

	return int64(resourceDefinition.VolumeDefinitions[volumeIndex].SizeKib * 1024), nil
}

// getResourceDefinitions returns all available resource definitions.
func (d *linstor) getResourceDefinitions() ([]linstorClient.ResourceDefinitionWithVolumeDefinition, error) {
	linstor, err := d.state.Linstor()
	if err != nil {
		return nil, err
	}

	// Get the resource definitions.
	resourceDefinitions, err := linstor.Client.ResourceDefinitions.GetAll(context.TODO(), linstorClient.RDGetAllRequest{
		WithVolumeDefinitions: false,
	})
	if err != nil {
		return nil, fmt.Errorf("Unable to get resource definitions: %w", err)
	}

	return resourceDefinitions, nil
}

// setResourceDefinitionProperties sets properties on the resource definition based on the volume config.
func (d *linstor) setResourceDefinitionProperties(vol Volume, resourceDefinitionName string) error {
	l := logger.AddContext(logger.Ctx{"volume": vol.Name(), "resourceDefinition": resourceDefinitionName})
	l.Debug("Setting resource definition properties", logger.Ctx{"config": vol.config})

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	// Set the base properties.
	overrideProps := map[string]string{
		LinstorAuxName:                        d.config[LinstorVolumePrefixConfigKey] + vol.name,
		LinstorAuxType:                        string(vol.volType),
		LinstorAuxContentType:                 string(vol.contentType),
		"DrbdOptions/Net/allow-two-primaries": "yes", // Required for mounting volumes simultaneously on two nodes when live migrating
	}

	// Parse and set properties derived from config.
	drbdProps, err := d.drbdPropsFromConfig(vol.config)
	if err != nil {
		return fmt.Errorf("Could parse config into DRBD options: %w", err)
	}

	for k, v := range drbdProps {
		overrideProps[k] = v
	}

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinitionName, linstorClient.GenericPropsModify{
		OverrideProps: overrideProps,
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	return nil
}

// updateResourceDefinitionupdates the resource definition with the given changed configs.
func (d *linstor) updateResourceDefinition(vol Volume, changedConfig map[string]string) error {
	l := logger.AddContext(logger.Ctx{"volume": vol.Name()})
	l.Debug("Updating resource definition", logger.Ctx{"changedchangedConfig": changedConfig})

	linstor, err := d.state.Linstor()
	if err != nil {
		return err
	}

	resourceDefinition, err := d.getResourceDefinition(vol, false)
	if err != nil {
		return err
	}

	// Parse and set properties to be overwritten.
	overrideProps, err := d.drbdPropsFromConfig(changedConfig)
	if err != nil {
		return fmt.Errorf("Could parse config into DRBD props: %w", err)
	}

	// Parse and set properties to be deleted.
	deleteProps := []string{}
	for key, value := range changedConfig {
		if value != "" {
			continue
		}

		prop, ok := drbdPropsMap[key]
		if ok {
			deleteProps = append(deleteProps, prop)
		}
	}

	err = linstor.Client.ResourceDefinitions.Modify(context.TODO(), resourceDefinition.Name, linstorClient.GenericPropsModify{
		OverrideProps: overrideProps,
		DeleteProps:   deleteProps,
	})
	if err != nil {
		return fmt.Errorf("Could not set properties on resource definition: %w", err)
	}

	return nil
}

// drbdPropsFromConfig creates a map of DRBD properties from the given volume or storage pool config.
func (d *linstor) drbdPropsFromConfig(config map[string]string) (map[string]string, error) {
	props := map[string]string{}

	for key, prop := range drbdPropsMap {
		value, changed := config[key]
		if !changed {
			continue
		}

		switch key {
		case DrbdAutoDiskfulConfigKey:
			duration, err := time.ParseDuration(value)
			if err != nil {
				return nil, err
			}

			props[prop] = strconv.Itoa(int(duration.Seconds()))
		case DrbdAutoAddQuorumTiebreakerConfigKey:
			if util.IsFalse(value) {
				props[prop] = "false"
			} else {
				props[prop] = "true"
			}

		default:
			props[prop] = value
		}
	}

	return props, nil
}

// getSnapshots retrieves all snapshots for a given resource definition name.
func (d *linstor) getSnapshots(resourceDefinitionName string) ([]linstorClient.Snapshot, error) {
	linstor, err := d.state.Linstor()
	if err != nil {
		return []linstorClient.Snapshot{}, err
	}

	snapshots, err := linstor.Client.Resources.GetSnapshots(context.TODO(), resourceDefinitionName)
	if err != nil {
		return snapshots, fmt.Errorf("Unable to get snapshots: %w", err)
	}

	return snapshots, nil
}

// rsyncMigrationType returns the migration types to use for a given content type.
func (d *linstor) rsyncMigrationType(contentType ContentType) localMigration.Type {
	var rsyncTransportType migration.MigrationFSType
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if util.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if IsContentBlock(contentType) {
		rsyncTransportType = migration.MigrationFSType_BLOCK_AND_RSYNC
	} else {
		rsyncTransportType = migration.MigrationFSType_RSYNC
	}

	return localMigration.Type{
		FSType:   rsyncTransportType,
		Features: rsyncFeatures,
	}
}

// parseVolumeType parses a string into a volume type.
func (d *linstor) parseVolumeType(s string) (*VolumeType, bool) {
	for _, volType := range d.Info().VolumeTypes {
		if s == string(volType) {
			return &volType, true
		}
	}

	return nil, false
}

// parseContentType parses a string into a volume type.
func (d *linstor) parseContentType(s string) (*ContentType, bool) {
	for _, contentType := range []ContentType{ContentTypeFS, ContentTypeBlock, ContentTypeISO} {
		if s == string(contentType) {
			return &contentType, true
		}
	}

	return nil, false
}

// generateUUIDWithPrefix generates a new UUID (without "-") and appends it to the configured volume prefix.
func (d *linstor) generateUUIDWithPrefix() string {
	return d.config[LinstorVolumePrefixConfigKey] + strings.ReplaceAll(uuid.NewString(), "-", "")
}
