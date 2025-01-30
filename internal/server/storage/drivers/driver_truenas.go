// package drivers

// type truenas struct {
//     common
// }

// var driverLoaded bool
// var tnaVersion string

// func (d *truenas) Info() Info {
// 	info := Info{
// 		Name:                         "truenas",
// 		Version:                      tnaVersion,
// 		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
// 		OptimizedImages:              true,
// 		OptimizedBackups:             true,
// 		PreservesInodes:              true,
// 		Remote:                       true,
// 		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
// 		VolumeMultiNode:              true,
// 		BlockBacking:                 false,
// 		RunningCopyFreeze:            false,
// 		DirectIO:                     false,
// 		MountedRoot:                  false,
// 		Buckets:                      false,
// 	}

// 	return info
// }

// func (d *truenas) load() error {
//     if driverLoaded {
//         return nil
//     }

//     // Get the version information.
//     if tnaVersion == "" {
//         version, err := d.version()
//         if err != nil {
//             return err
//         }

//         tnaVersion = version
//     }

//     driverLoaded = true
//     return err
// }

// func (d *truenas) version() (string, error) {
// 	out, err = subprocess.RunCommand("truenas-admin", "version")
// 	if err == nil {
// 		return strings.TrimSpace(string(out)), nil
// 	}

// 	return "", fmt.Errorf("Could not determine TrueNAS driver version (truenas-admin was missing)")
// }

package drivers

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/lxc/incus/v6/internal/migration"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

var tnVersion string
var tnLoaded bool

// TODO: these flags are not needed once we stop using earlier versions.
var tnHasLoginFlags bool // 0.1.1

var tnDefaultSettings = map[string]string{
	"relatime":   "on",
	"mountpoint": "legacy",
	"setuid":     "on",
	"exec":       "on",
	"devices":    "on",
	"acltype":    "posixacl",
	"xattr":      "sa",
}

type truenas struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *truenas) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil, //d.patchDropBlockVolumeFilesystemExtension,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if tnLoaded {
		return nil
	}

	// Validate the needed tools are present.
	for _, tool := range []string{tnToolName} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// Get the version information.
	if tnVersion == "" {
		version, err := d.version()
		if err != nil {
			return err
		}

		tnVersion = version
	}

	// Decide whether we can use features added by 0.1.1
	ver011, err := version.Parse("0.1.1")
	if err != nil {
		return err
	}

	ourVer, err := version.Parse(tnVersion)
	if err != nil {
		return err
	}

	// If 0.1.1 we can use login flags (api-key, url, key-file)
	// TODO: remove this later.
	if ourVer.Compare(ver011) >= 0 {
		tnHasLoginFlags = true
	}

	tnLoaded = true
	// before, _ := d.datasetExists("dozer/created")
	// if !before {
	// 	d.createDataset("dozer/created")
	// }
	// after, _ := d.datasetExists("dozer/created")
	// if after {
	// 	d.deleteDataset("dozer/created")
	// }

	return nil
}

// Info returns info about the driver and its environment.
func (d *truenas) Info() Info {
	info := Info{
		Name:                         "truenas",
		Version:                      tnVersion,
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              true,
		OptimizedBackups:             true,
		PreservesInodes:              true,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		VolumeMultiNode:              d.isRemote(),
		BlockBacking:                 util.IsTrue(d.config["volume.zfs.block_mode"]),
		RunningCopyFreeze:            false,
		DirectIO:                     zfsDirectIO,
		MountedRoot:                  false,
		Buckets:                      false,
	}

	return info
}

// ensureInitialDatasets creates missing initial datasets or configures existing ones with current policy.
// Accepts warnOnExistingPolicyApplyError argument, if true will warn rather than fail if applying current policy
// to an existing dataset fails.
func (d truenas) ensureInitialDatasets(warnOnExistingPolicyApplyError bool) error {
	// args := make([]string, 0, len(zfsDefaultSettings))
	// for k, v := range zfsDefaultSettings {
	// 	args = append(args, fmt.Sprintf("%s=%s", k, v))
	// }

	// err := d.setDatasetProperties(d.config["zfs.pool_name"], args...)
	// if err != nil {
	// 	if warnOnExistingPolicyApplyError {
	// 		d.logger.Warn("Failed applying policy to existing dataset", logger.Ctx{"dataset": d.config["zfs.pool_name"], "err": err})
	// 	} else {
	// 		return fmt.Errorf("Failed applying policy to existing dataset %q: %w", d.config["zfs.pool_name"], err)
	// 	}
	// }

	// for _, dataset := range d.initialDatasets() {
	// 	properties := []string{"mountpoint=legacy"}
	// 	if slices.Contains([]string{"virtual-machines", "deleted/virtual-machines"}, dataset) {
	// 		properties = append(properties, "volmode=none")
	// 	}

	// 	datasetPath := filepath.Join(d.config["zfs.pool_name"], dataset)
	// 	exists, err := d.datasetExists(datasetPath)
	// 	if err != nil {
	// 		return err
	// 	}

	// 	if exists {
	// 		err = d.setDatasetProperties(datasetPath, properties...)
	// 		if err != nil {
	// 			if warnOnExistingPolicyApplyError {
	// 				d.logger.Warn("Failed applying policy to existing dataset", logger.Ctx{"dataset": datasetPath, "err": err})
	// 			} else {
	// 				return fmt.Errorf("Failed applying policy to existing dataset %q: %w", datasetPath, err)
	// 			}
	// 		}
	// 	} else {
	// 		err = d.createDataset(datasetPath, properties...)
	// 		if err != nil {
	// 			return fmt.Errorf("Failed creating dataset %q: %w", datasetPath, err)
	// 		}
	// 	}
	// }

	return nil
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *truenas) FillConfig() error {

	// set host url
	if d.config["truenas.url"] == "" && d.config["truenas.host"] != "" {
		d.config["truenas.url"] = fmt.Sprintf("wss://%s/api/current", d.config["truenas.host"])
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *truenas) Create() error {

	err := d.FillConfig()

	exists, err := d.datasetExists(d.config["source"])
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf(`Provided source: %s, does not exist on TrueNAS host`, d.config["source"])
	}

	d.config["truenas.dataset"] = d.config["source"] + "/" + d.name

	// Handle a dataset.
	exists, err = d.datasetExists(d.config["truenas.dataset"])
	if err != nil {
		return err
	}

	if !exists {
		err := d.createDataset(d.config["truenas.dataset"])
		if err != nil {
			return err
		}
	}

	// Confirm that the existing pool/dataset is all empty.
	datasets, err := d.getDatasets(d.config["truenas.dataset"], "all")
	if err != nil {
		return err
	}

	if len(datasets) > 0 {
		return fmt.Errorf(`Provided remote TrueNAS dataset isn't empty`, d.config["truenas.dataset"])
	}

	// Setup revert in case of problems
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { _ = d.Delete(nil) })

	// Apply our default configuration.
	err = d.ensureInitialDatasets(false)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Delete removes the storage pool from the storage device.
func (d *truenas) Delete(op *operations.Operation) error {
	// Check if the dataset/pool is already gone.
	exists, err := d.datasetExists(d.config["truenas.dataset"])
	if err != nil {
		return err
	}

	if exists {
		// Confirm that nothing's been left behind
		datasets, err := d.getDatasets(d.config["truenas.dataset"], "all")
		if err != nil {
			return err
		}

		initialDatasets := d.initialDatasets()
		for _, dataset := range datasets {
			dataset = strings.TrimPrefix(dataset, "/")

			if slices.Contains(initialDatasets, dataset) {
				continue
			}

			fields := strings.Split(dataset, "/")
			if len(fields) > 1 {
				return fmt.Errorf("TrueNAS pool has leftover datasets: %s", dataset)
			}
		}

		// Delete the dataset.
		_, err = d.runTool("dataset", "delete", "-r", d.config["truenas.dataset"])
		if err != nil {
			return err
		}
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	// // Delete any loop file we may have used
	// loopPath := loopFilePath(d.name)
	// err = os.Remove(loopPath)
	// if err != nil && !errors.Is(err, fs.ErrNotExist) {
	// 	return fmt.Errorf("Failed to remove '%s': %w", loopPath, err)
	// }

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *truenas) Validate(config map[string]string) error {
	// rules := map[string]func(value string) error{
	// 	"size":          validate.Optional(validate.IsSize),
	// 	"zfs.pool_name": validate.IsAny,
	// 	"zfs.clone_copy": validate.Optional(func(value string) error {
	// 		if value == "rebase" {
	// 			return nil
	// 		}

	// 		return validate.IsBool(value)
	// 	}),
	// 	"zfs.export": validate.Optional(validate.IsBool),
	// }

	return nil //d.validatePool(config, rules, d.commonVolumeRules())
}

// Update applies any driver changes required from a configuration change.
func (d *truenas) Update(changedConfig map[string]string) error {
	// _, ok := changedConfig["zfs.pool_name"]
	// if ok {
	// 	return fmt.Errorf("zfs.pool_name cannot be modified")
	// }

	// size, ok := changedConfig["size"]
	// if ok {
	// 	// Figure out loop path
	// 	loopPath := loopFilePath(d.name)

	// 	_, devices := d.parseSource()
	// 	if len(devices) != 1 || devices[0] != loopPath {
	// 		return fmt.Errorf("Cannot resize non-loopback pools")
	// 	}

	// 	// Resize loop file
	// 	f, err := os.OpenFile(loopPath, os.O_RDWR, 0600)
	// 	if err != nil {
	// 		return err
	// 	}

	// 	defer func() { _ = f.Close() }()

	// 	sizeBytes, _ := units.ParseByteSizeString(size)

	// 	err = f.Truncate(sizeBytes)
	// 	if err != nil {
	// 		return err
	// 	}

	// 	_, err = subprocess.RunCommand("zpool", "online", "-e", d.config["zfs.pool_name"], loopPath)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	return nil
}

// importPool the storage pool.
func (d *truenas) importPool() (bool, error) {
	// 	if d.config["zfs.pool_name"] == "" {
	// 		return false, fmt.Errorf("Cannot mount pool as %q is not specified", "zfs.pool_name")
	// 	}

	// 	// Check if already setup.
	// 	exists, err := d.datasetExists(d.config["zfs.pool_name"])
	// 	if err != nil {
	// 		return false, err
	// 	}

	// 	if exists {
	// 		return false, nil
	// 	}

	// 	// Check if the pool exists.
	// 	poolName := strings.Split(d.config["zfs.pool_name"], "/")[0]
	// 	exists, err = d.datasetExists(poolName)
	// 	if err != nil {
	// 		return false, err
	// 	}

	// 	if exists {
	// 		return false, fmt.Errorf("ZFS zpool exists but dataset is missing")
	// 	}

	// 	// Import the pool.
	// 	if filepath.IsAbs(d.config["source"]) {
	// 		disksPath := internalUtil.VarPath("disks")
	// 		_, err := subprocess.RunCommand("zpool", "import", "-f", "-d", disksPath, poolName)
	// 		if err != nil {
	// 			return false, err
	// 		}
	// 	} else {
	// 		_, err := subprocess.RunCommand("zpool", "import", poolName)
	// 		if err != nil {
	// 			return false, err
	// 		}
	// 	}

	// 	// Check that the dataset now exists.
	// 	exists, err = d.datasetExists(d.config["zfs.pool_name"])
	// 	if err != nil {
	// 		return false, err
	// 	}

	// 	if !exists {
	// 		return false, fmt.Errorf("ZFS zpool exists but dataset is missing")
	// 	}

	// 	// We need to explicitly import the keys here so containers can start. This
	// 	// is always needed because even if the admin has set up auto-import of
	// 	// keys on the system, because incus manually imports and exports the pools
	// 	// the keys can get unloaded.
	// 	//
	// 	// We could do "zpool import -l" to request the keys during import, but by
	// 	// doing it separately we know that the key loading specifically failed and
	// 	// not some other operation. If a user has keylocation=prompt configured,
	// 	// this command will fail and the pool will fail to load.
	// 	_, err = subprocess.RunCommand("zfs", "load-key", "-r", d.config["zfs.pool_name"])
	// 	if err != nil {
	// 		_, _ = d.Unmount()
	// 		return false, fmt.Errorf("Failed to load keys for ZFS dataset %q: %w", d.config["zfs.pool_name"], err)
	// 	}

	return true, nil
}

// Mount mounts the storage pool.
func (d *truenas) Mount() (bool, error) {
	// 	// Import the pool if not already imported.
	// 	imported, err := d.importPool()
	// 	if err != nil {
	// 		return false, err
	// 	}

	// 	// Apply our default configuration.
	// 	err = d.ensureInitialDatasets(true)
	// 	if err != nil {
	// 		return false, err
	// 	}

	return false, nil //imported, nil
}

// Unmount unmounts the storage pool.
func (d *truenas) Unmount() (bool, error) {
	// // Skip if zfs.export config is set to false
	// if util.IsFalse(d.config["zfs.export"]) {
	// 	return false, nil
	// }

	// // Skip if using a dataset and not a full pool.
	// if strings.Contains(d.config["zfs.pool_name"], "/") {
	// 	return false, nil
	// }

	// // Check if already unmounted.
	// exists, err := d.datasetExists(d.config["zfs.pool_name"])
	// if err != nil {
	// 	return false, err
	// }

	// if !exists {
	// 	return false, nil
	// }

	// // Export the pool.
	// poolName := strings.Split(d.config["zfs.pool_name"], "/")[0]
	// _, err = subprocess.RunCommand("zpool", "export", poolName)
	// if err != nil {
	// 	return false, err
	// }

	return true, nil
}

func (d *truenas) GetResources() (*api.ResourcesStoragePool, error) {
	// // Get the total amount of space.
	// availableStr, err := d.getDatasetProperty(d.config["zfs.pool_name"], "available")
	// if err != nil {
	// 	return nil, err
	// }

	// available, err := strconv.ParseUint(strings.TrimSpace(availableStr), 10, 64)
	// if err != nil {
	// 	return nil, err
	// }

	// // Get the used amount of space.
	// usedStr, err := d.getDatasetProperty(d.config["zfs.pool_name"], "used")
	// if err != nil {
	// 	return nil, err
	// }

	// used, err := strconv.ParseUint(strings.TrimSpace(usedStr), 10, 64)
	// if err != nil {
	// 	return nil, err
	// }

	// // Build the struct.
	// // Inode allocation is dynamic so no use in reporting them.
	// res := api.ResourcesStoragePool{}
	// res.Space.Total = used + available
	// res.Space.Used = used

	//return &res, nil
	return nil, nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *truenas) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []localMigration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if util.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	// Detect ZFS features.
	features := []string{migration.ZFSFeatureMigrationHeader, "compress"}

	if contentType == ContentTypeFS {
		features = append(features, migration.ZFSFeatureZvolFilesystems)
	}

	if IsContentBlock(contentType) {
		return []localMigration.Type{
			{
				FSType:   migration.MigrationFSType_ZFS,
				Features: features,
			},
			{
				FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	if refresh && !copySnapshots {
		return []localMigration.Type{
			{
				FSType:   migration.MigrationFSType_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	return []localMigration.Type{
		{
			FSType:   migration.MigrationFSType_ZFS,
			Features: features,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}

// patchDropBlockVolumeFilesystemExtension removes the filesystem extension (e.g _ext4) from VM image block volumes.
func (d *truenas) patchDropBlockVolumeFilesystemExtension() error {
	poolName, ok := d.config["zfs.pool_name"]
	if !ok {
		poolName = d.name
	}

	out, err := subprocess.RunCommand("zfs", "list", "-H", "-r", "-o", "name", "-t", "volume", fmt.Sprintf("%s/images", poolName))
	if err != nil {
		return fmt.Errorf("Failed listing images: %w", err)
	}

	for _, volume := range strings.Split(out, "\n") {
		fields := strings.SplitN(volume, fmt.Sprintf("%s/images/", poolName), 2)

		if len(fields) != 2 || fields[1] == "" {
			continue
		}

		// Ignore non-block images, and images without filesystem extension
		if !strings.HasSuffix(fields[1], ".block") || !strings.Contains(fields[1], "_") {
			continue
		}

		// Rename zfs dataset. Snapshots will automatically be renamed.
		newName := fmt.Sprintf("%s/images/%s.block", poolName, strings.Split(fields[1], "_")[0])

		_, err = subprocess.RunCommand("zfs", "rename", volume, newName)
		if err != nil {
			return fmt.Errorf("Failed renaming zfs dataset: %w", err)
		}
	}

	return nil
}

// roundVolumeBlockSizeBytes returns sizeBytes rounded up to the next multiple
// of `vol`'s "zfs.blocksize".
func (d *truenas) roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) (int64, error) {
	minBlockSize, err := units.ParseByteSizeString(vol.ExpandedConfig("zfs.blocksize"))

	// minBlockSize will be 0 if zfs.blocksize=""
	if minBlockSize <= 0 || err != nil {
		// 16KiB is the default volblocksize
		minBlockSize = 16 * 1024
	}

	return roundAbove(minBlockSize, sizeBytes), nil
}
