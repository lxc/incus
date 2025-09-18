package drivers

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/lxc/incus/v6/internal/migration"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

var (
	tnVersion string
	tnLoaded  bool
)

var tnDefaultSettings = map[string]string{
	"atime":     "off",
	"exec":      "on",
	"acltype":   "posix",
	"aclmode":   "discard",
	"comments":  "Managed by Incus.TrueNAS", // these are set in createDataset
	"managedby": "incus.truenas",
}

type truenas struct {
	common

	// Temporary cache (typically lives for the duration of a query).
	cache   map[string]map[string]int64
	cacheMu sync.Mutex
}

func (d *truenas) isVersionGE(thisVersion version.DottedVersion, thatVersion string) bool {
	ver, err := version.Parse(thatVersion)
	if err != nil {
		return false
	}

	return (thisVersion.Compare(ver) >= 0)
}

func (d *truenas) initVersionAndCapabilities() error {
	// Get the version information.
	if tnVersion == "" {
		ver, err := d.version()
		if err != nil {
			return err
		}

		tnVersion = ver
	}

	ourVer, err := version.Parse(tnVersion)
	if err != nil {
		return err
	}

	// this same logic can be used for feature detection based on versions.
	if !d.isVersionGE(*ourVer, tnMinVersion) {
		return fmt.Errorf("TrueNAS driver requires %s v%s or later, but the currently installed version is v%s", tnToolName, tnMinVersion, tnVersion)
	}

	return nil
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *truenas) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if tnLoaded {
		return nil
	}

	// Handle IncusOS services.
	if d.state.OS.IncusOS != nil {
		ok, err := d.state.OS.IncusOS.IsServiceEnabled("iscsi")
		if err != nil {
			return err
		}

		if !ok {
			return errors.New("IncusOS service \"iscsi\" isn't currently enabled")
		}
	}

	// Validate the needed tools are present.
	for _, tool := range []string{tnToolName} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// also tests for available features
	err := d.initVersionAndCapabilities()
	if err != nil {
		return err
	}

	tnLoaded = true

	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *truenas) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *truenas) Info() Info {
	info := Info{
		Name:                         "truenas",
		Version:                      tnVersion,
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              true,
		OptimizedBackups:             false,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		VolumeMultiNode:              false, // can only use the same volume if its read-only.d.isRemote(),
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     false,
		IOUring:                      false,
		MountedRoot:                  false,
		Buckets:                      false,
	}

	return info
}

// ensureInitialDatasets creates missing initial datasets or configures existing ones with current policy.
// Accepts warnOnExistingPolicyApplyError argument, if true will warn rather than fail if applying current policy
// to an existing dataset fails.
func (d *truenas) ensureInitialDatasets(warnOnExistingPolicyApplyError bool) error {
	args := make([]string, 0, len(tnDefaultSettings))
	for k, v := range tnDefaultSettings {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}

	if d.config["truenas.dataset"] == "" {
		return nil
	}

	err := d.setDatasetProperties(d.config["truenas.dataset"], args...)
	if err != nil {
		if !warnOnExistingPolicyApplyError {
			return fmt.Errorf("Failed applying policy to existing dataset %q: %w", d.config["truenas.dataset"], err)
		}

		d.logger.Warn("Failed applying policy to existing dataset", logger.Ctx{"dataset": d.config["truenas.dataset"], "err": err})
	}

	datasets := d.initialDatasets()
	fullDatasetPaths := make([]string, len(datasets))
	for i := 0; i < len(datasets); i++ {
		fullDatasetPaths[i] = filepath.Join(d.config["truenas.dataset"], datasets[i])
	}

	properties := []string{}

	shouldCreateMissingDatasets := true
	return d.updateDatasets(fullDatasetPaths, shouldCreateMissingDatasets, properties...)
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *truenas) FillConfig() error {
	// populate source if not already present
	if d.config["truenas.dataset"] != "" && d.config["source"] == "" {
		d.config["source"] = d.config["truenas.dataset"]
	}

	err := d.parseSource()
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) parseSource() error {
	// fill config may modify.
	sourceStr := d.config["source"]
	var host, path string

	if strings.HasPrefix(sourceStr, "[") {
		// IPv6 with brackets
		endBracket := strings.Index(sourceStr, "]")
		if endBracket == -1 || endBracket+1 >= len(sourceStr) || sourceStr[endBracket+1] != ':' {
			// Malformed, treat whole string as path
			host = ""
			path = sourceStr
		} else {
			host = sourceStr[:endBracket+1]
			path = sourceStr[endBracket+2:] // skip over "]:"
		}
	} else {
		// Try normal IPv4/hostname
		h, p, ok := strings.Cut(sourceStr, ":")
		if ok {
			host = h
			path = p
		} else {
			// No colon: whole thing is path
			host = ""
			path = sourceStr
		}
	}

	if path == "" || filepath.IsAbs(path) {
		return errors.New(`TrueNAS Driver requires "source" to be specified using the format: [<remote host>:]<remote pool>[[/<remote dataset>]...][/]`)
	}

	// a pool... means we create a dataset in the root
	if !strings.Contains(path, "/") {
		path += "/"
	}

	// a trailing slash means use the storage pool name as the dataset
	if strings.HasSuffix(path, "/") {
		path += d.name
	}

	d.config["truenas.dataset"] = path
	if host != "" {
		if d.config["truenas.host"] != "" {
			host = d.config["truenas.host"]
		}

		source := fmt.Sprintf("%s:%s", host, path)
		d.config["truenas.host"] = host
		d.config["source"] = source
	} else {
		d.config["source"] = path
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *truenas) Create() error {
	// Store the provided source as we are likely to be mangling it.
	d.config["volatile.initial_source"] = d.config["source"]

	err := d.FillConfig()
	if err != nil {
		return err
	}

	// create pool dataset
	exists, err := d.datasetExists(d.config["truenas.dataset"])
	if err != nil {
		return err
	}

	if !exists {
		err = d.createDataset(d.config["truenas.dataset"])
		if err != nil {
			return fmt.Errorf("Failed to create storage pool on TrueNAS host: %s, err: %w", d.config["source"], err)
		}
	} else if util.IsFalseOrEmpty(d.config["truenas.force_reuse"]) {
		// Confirm that the existing pool/dataset is all empty.
		datasets, err := d.getDatasets(d.config["truenas.dataset"], "all")
		if err != nil {
			return err
		}

		if len(datasets) > 0 {
			return fmt.Errorf(`Remote TrueNAS dataset isn't empty: %s`, d.config["truenas.dataset"])
		}
	}

	// Setup revert in case of problems
	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { _ = d.Delete(nil) })

	err = d.verifyIscsiFunctionality(false) // ensureSetup
	if err != nil {
		return fmt.Errorf("Unable to verify TrueNAS iSCSI service: %v", err)
	}

	reverter.Success()
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
		err = d.deleteDataset(d.config["truenas.dataset"], true)
		if err != nil {
			return err
		}
	}

	// On delete, wipe everything in the directory.
	err = wipeDirectory(GetPoolMountPath(d.name))
	if err != nil {
		return err
	}

	return nil
}

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *truenas) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// only truenas.dataset is required. the tool has default behaviour/connections defined.
		"source":          validate.IsAny, // can be used as a shortcut to specify dataset and optionally host.
		"truenas.dataset": validate.IsAny,

		// global flags for the tool
		"truenas.allow_insecure": validate.Optional(validate.IsBool),
		"truenas.api_key":        validate.IsAny,
		"truenas.config":         validate.IsAny,
		"truenas.host":           validate.IsAny,

		// flags for the tool's iscsi commands
		"truenas.initiator": validate.IsAny,
		"truenas.portal":    validate.IsAny,

		// controls behaviour of the driver
		"truenas.clone_copy":  validate.Optional(validate.IsBool),
		"truenas.force_reuse": validate.Optional(validate.IsBool),
	}

	return d.validatePool(config, rules, d.commonVolumeRules())
}

// Update applies any driver changes required from a configuration change.
func (d *truenas) Update(changedConfig map[string]string) error {
	_, ok := changedConfig["truenas.dataset"]
	if ok {
		return errors.New("truenas.dataset cannot be modified")
	}

	// prop changes we want to accept
	props := []string{
		"truenas.allow_insecure",
		"truenas.api_key",
		"truenas.config",
		"truenas.host",
		"truenas.initiator",
		"truenas.portal",
		"truenas.clone_copy",
		"truenas.force_reuse",
	}

	for _, prop := range props {
		value, ok := changedConfig[prop]
		if ok {
			d.config[prop] = value
		}
	}

	return nil
}

// Mount mounts the storage pool.
func (d *truenas) Mount() (bool, error) {
	// verify pool dataset exists
	exists, err := d.datasetExists(d.config["truenas.dataset"])
	if err != nil {
		return false, err
	}

	if !exists {
		return false, fmt.Errorf("TrueNAS host is responding, but dataset is missing %s:%s", d.config["truenas.host"], d.config["truenas.dataset"])
	}

	// Apply our default configuration.
	err = d.ensureInitialDatasets(true)
	if err != nil {
		return false, err
	}

	// As we have already created the storage pool, and it exists on the host, presumably we already had iscsi setup in the past, so restore it if necessary.
	err = d.verifyIscsiFunctionality(true)
	if err != nil {
		return false, err
	}

	return false, nil
}

// Unmount unmounts the storage pool.
func (d *truenas) Unmount() (bool, error) {
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *truenas) GetResources() (*api.ResourcesStoragePool, error) {
	// Get the total amount of space and the used amount of space.
	props, err := d.getDatasetProperties(d.config["truenas.dataset"], []string{"available", "used"})
	if err != nil {
		return nil, err
	}

	// Parse the total amount of space.
	availableStr := props["available"]
	available, err := strconv.ParseUint(strings.TrimSpace(availableStr), 10, 64)
	if err != nil {
		return nil, err
	}

	// Parse the used amount of space.
	usedStr := props["used"]
	used, err := strconv.ParseUint(strings.TrimSpace(usedStr), 10, 64)
	if err != nil {
		return nil, err
	}

	// Build the struct.
	// Inode allocation is dynamic so no use in reporting them.
	res := api.ResourcesStoragePool{}
	res.Space.Total = used + available
	res.Space.Used = used

	return &res, nil
}

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *truenas) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool, clusterMove bool, storageMove bool) []localMigration.Type {
	// TODO: investigate "storageMove" that came from the linstor driver.

	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if util.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if IsContentBlock(contentType) {
		return []localMigration.Type{
			// TODO: optimized
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
		// TODO: optimized
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}

// roundVolumeBlockSizeBytes returns sizeBytes rounded up to the next multiple
// of `vol`'s "truenas.blocksize".
func (d *truenas) roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) (int64, error) {
	minBlockSize, err := units.ParseByteSizeString(vol.ExpandedConfig("truenas.blocksize"))

	// minBlockSize will be 0 if truenas.blocksize=""
	if minBlockSize <= 0 || err != nil {
		minBlockSize = tnDefaultVolblockSize // 16KiB
	}

	return roundAbove(minBlockSize, sizeBytes), nil
}
