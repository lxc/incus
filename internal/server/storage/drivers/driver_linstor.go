package drivers

import (
	"fmt"
	"strconv"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

var (
	linstorVersion string
	linstorLoaded  bool
)

// linstor represents the Linstor storage driver.
type linstor struct {
	common
}

func (d *linstor) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if linstorLoaded {
		return nil
	}

	// Validate the DRBD minimum version. The module should be already loaded by the
	// Linstor satellite service.
	drbdVer, err := d.drbdVersion()
	if err != nil {
		return err
	}

	ver, err := version.Parse(drbdVer)
	if err != nil {
		return fmt.Errorf("Could not determine DRBD module version: %w", err)
	}

	if ver.Major < 9 {
		return fmt.Errorf("Could not load Linstor driver: Linstor requires DRBD version 9.0 to be loaded, got: %s", ver)
	}

	// Get the controller version.
	controllerVer, err := d.controllerVersion()
	if err != nil {
		return err
	}

	linstorVersion = controllerVer + " / " + drbdVer
	linstorLoaded = true

	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *linstor) isRemote() bool {
	return true
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *linstor) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		LinstorResourceGroupNameConfigKey:        validate.IsAny,
		LinstorResourceGroupPlaceCountConfigKey:  validate.Optional(validate.IsUint32),
		LinstorResourceGroupStoragePoolConfigKey: validate.IsAny,
		LinstorVolumePrefixConfigKey:             validate.IsShorterThan(24),
		"volatile.pool.pristine":                 validate.IsAny,
	}

	return d.validatePool(config, rules, nil)
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *linstor) FillConfig() error {
	if d.config[LinstorResourceGroupPlaceCountConfigKey] == "" {
		d.config[LinstorResourceGroupPlaceCountConfigKey] = LinstorDefaultResourceGroupPlaceCount
	}

	if d.config[LinstorVolumePrefixConfigKey] == "" {
		d.config[LinstorVolumePrefixConfigKey] = LinstorDefaultVolumePrefix
	}

	return nil
}

// Create is called during storage pool creation.
func (d *linstor) Create() error {
	d.logger.Debug("Creating Linstor storage pool")
	rev := revert.New()
	defer rev.Fail()

	// Track the initial source.
	d.config["volatile.initial_source"] = d.config["source"]

	// Fill default config values.
	err := d.FillConfig()
	if err != nil {
		return fmt.Errorf("Could not create Linstor storage pool: %w", err)
	}

	// Quick check of conflicting values.
	if d.config["source"] != "" && d.config[LinstorResourceGroupNameConfigKey] != "" && d.config["source"] != d.config[LinstorResourceGroupNameConfigKey] {
		return fmt.Errorf(`The "source" and %q property must not differ for LINSTOR storage pools`, LinstorResourceGroupNameConfigKey)
	}

	// If a source is provided, use it as the resource group name.
	if d.config["source"] != "" {
		d.config[LinstorResourceGroupNameConfigKey] = d.config["source"]
	} else if d.config[LinstorResourceGroupNameConfigKey] == "" {
		d.config[LinstorResourceGroupNameConfigKey] = d.name
	}

	d.config["source"] = d.config[LinstorResourceGroupNameConfigKey]

	resourceGroupExists, err := d.resourceGroupExists()
	if err != nil {
		return fmt.Errorf("Could not create Linstor storage pool: %w", err)
	}

	if !resourceGroupExists {
		// Create new resource group.
		d.logger.Debug("Resource group does not exist. Creating one")
		err := d.createResourceGroup()
		if err != nil {
			return fmt.Errorf("Could not create Linstor storage pool: %w", err)
		}

		rev.Add(func() { _ = d.deleteResourceGroup() })

		d.config["volatile.pool.pristine"] = "true"
	} else {
		d.logger.Debug("Resource group already exists. Using an existing one")
		resourceGroup, err := d.getResourceGroup()
		if err != nil {
			return fmt.Errorf("Could not create Linstor storage pool: %w", err)
		}

		d.config[LinstorResourceGroupPlaceCountConfigKey] = strconv.Itoa(int(resourceGroup.SelectFilter.PlaceCount))
		d.config[LinstorResourceGroupStoragePoolConfigKey] = resourceGroup.SelectFilter.StoragePool
	}

	rev.Success()
	return nil
}

// ListVolumes returns a list of volumes in storage pool.
func (d *linstor) ListVolumes() ([]Volume, error) {
	// TODO: implement volume listing
	return []Volume{}, nil
}

// Delete removes the storage pool from the storage device.
func (d *linstor) Delete(op *operations.Operation) error {
	d.logger.Debug("Deleting Linstor storage pool")

	// Test if the resource group exists.
	resourceGroupExists, err := d.resourceGroupExists()
	if err != nil {
		return fmt.Errorf("Could not check if Linstor resource group exists: %w", err)
	}

	if !resourceGroupExists {
		d.logger.Warn("Resource group does not exist")
	} else {
		// Check whether we own the resource group and only remove in this case.
		if util.IsTrue(d.config["volatile.pool.pristine"]) {
			// Delete the resource group pool.
			err := d.deleteResourceGroup()
			if err != nil {
				return err
			}

			d.logger.Debug("Deleted Linstor resource group")
		} else {
			d.logger.Debug("Linstor resource group is not owned by Incus, skipping delete")
		}
	}

	// If the user completely destroyed it, call it done.
	if !util.PathExists(GetPoolMountPath(d.name)) {
		return nil
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	return nil
}

// GetResources returns the pool resource usage information.
func (d *linstor) GetResources() (*api.ResourcesStoragePool, error) {
	// TODO: implement getting resource usage
	return nil, ErrNotSupported
}

// Info returns info about the driver and its environment.
func (d *linstor) Info() Info {
	return Info{
		Name:                         "linstor",
		Version:                      linstorVersion,
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		Buckets:                      false,
		Remote:                       d.isRemote(),
		VolumeMultiNode:              false, // DRBD uses an active-passive replication paradigm, so we cannot use the same volume concurrently in multiple nodes.
		OptimizedImages:              false,
		OptimizedBackups:             false,
		OptimizedBackupHeader:        false,
		PreservesInodes:              false,
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
		Deactivate:                   false,
	}
}

// Mount mounts the storage pool.
func (d *linstor) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage pool.
func (d *linstor) Unmount() (bool, error) {
	return true, nil
}

// Update applies any driver changes required from a configuration change.
func (d *linstor) Update(changedConfig map[string]string) error {
	return ErrNotSupported
}
