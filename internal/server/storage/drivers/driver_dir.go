package drivers

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/operations"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

type dir struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *dir) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	return nil
}

// Info returns info about the driver and its environment.
func (d *dir) Info() Info {
	return Info{
		Name:                         "dir",
		Version:                      "1",
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              false,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 false,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  true,
		Buckets:                      true,
	}
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *dir) FillConfig() error {
	// Set default source if missing.
	if d.config["source"] == "" {
		d.config["source"] = GetPoolMountPath(d.name)
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *dir) Create() error {
	err := d.FillConfig()
	if err != nil {
		return err
	}

	sourcePath := d.config["source"]

	if !util.PathExists(sourcePath) {
		return fmt.Errorf("Source path '%s' doesn't exist", sourcePath)
	}

	// Check that if within INCUS_DIR, we're at our expected spot.
	cleanSource := filepath.Clean(sourcePath)
	if strings.HasPrefix(cleanSource, internalUtil.VarPath()) && cleanSource != GetPoolMountPath(d.name) {
		return fmt.Errorf("Source path '%s' is within the Incus directory", cleanSource)
	}

	// Check that the path is currently empty.
	isEmpty, err := internalUtil.PathIsEmpty(sourcePath)
	if err != nil {
		return err
	}

	if !isEmpty {
		return fmt.Errorf("Source path '%s' isn't empty", sourcePath)
	}

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *dir) Delete(op *operations.Operation) error {
	// On delete, wipe everything in the directory.
	err := wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// Unmount the path.
	_, err = d.Unmount()
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *dir) Validate(config map[string]string) error {
	return d.validatePool(config, nil, nil)
}

// Update applies any driver changes required from a configuration change.
func (d *dir) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *dir) Mount() (bool, error) {
	path := GetPoolMountPath(d.name)
	sourcePath := d.config["source"]

	// Check if we're dealing with an external mount.
	if sourcePath == path {
		return false, nil
	}

	// Check if already mounted.
	if sameMount(sourcePath, path) {
		return false, nil
	}

	// Setup the bind-mount.
	err := TryMount(sourcePath, path, "none", unix.MS_BIND, "")
	if err != nil {
		return false, err
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *dir) Unmount() (bool, error) {
	path := GetPoolMountPath(d.name)

	// Check if we're dealing with an external mount.
	if d.config["source"] == path {
		return false, nil
	}

	// Unmount until nothing is left mounted.
	return forceUnmount(path)
}

// GetResources returns the pool resource usage information.
func (d *dir) GetResources() (*api.ResourcesStoragePool, error) {
	return genericVFSGetResources(d)
}
