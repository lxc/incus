package drivers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/revert"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	localMigration "github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

var cephVersion string
var cephLoaded bool

type ceph struct {
	common
}

// load is used to run one-time action per-driver rather than per-pool.
func (d *ceph) load() error {
	// Register the patches.
	d.patches = map[string]func() error{
		"storage_lvm_skipactivation":                         nil,
		"storage_missing_snapshot_records":                   nil,
		"storage_delete_old_snapshot_records":                nil,
		"storage_zfs_drop_block_volume_filesystem_extension": nil,
		"storage_prefix_bucket_names_with_project":           nil,
	}

	// Done if previously loaded.
	if cephLoaded {
		return nil
	}

	// Validate the required binaries.
	for _, tool := range []string{"ceph", "rbd"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("Required tool '%s' is missing", tool)
		}
	}

	// Detect and record the version.
	if cephVersion == "" {
		out, err := subprocess.RunCommand("rbd", "--version")
		if err != nil {
			return err
		}

		out = strings.TrimSpace(out)

		fields := strings.Split(out, " ")
		if strings.HasPrefix(out, "ceph version ") && len(fields) > 2 {
			cephVersion = fields[2]
		} else {
			cephVersion = out
		}
	}

	cephLoaded = true
	return nil
}

// isRemote returns true indicating this driver uses remote storage.
func (d *ceph) isRemote() bool {
	return true
}

// Info returns info about the driver and its environment.
func (d *ceph) Info() Info {
	return Info{
		Name:                         "ceph",
		Version:                      cephVersion,
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              true,
		PreservesInodes:              false,
		Remote:                       d.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		BlockBacking:                 true,
		RunningCopyFreeze:            true,
		DirectIO:                     true,
		IOUring:                      true,
		MountedRoot:                  false,
	}
}

// getPlaceholderVolume returns the volume used to indicate if the pool is in use.
func (d *ceph) getPlaceholderVolume() Volume {
	return NewVolume(d, d.name, VolumeType("incus"), ContentTypeFS, d.config["ceph.osd.pool_name"], nil, nil)
}

// FillConfig populates the storage pool's configuration file with the default values.
func (d *ceph) FillConfig() error {
	if d.config["ceph.cluster_name"] == "" {
		d.config["ceph.cluster_name"] = CephDefaultCluster
	}

	if d.config["ceph.user.name"] == "" {
		d.config["ceph.user.name"] = CephDefaultUser
	}

	if d.config["ceph.osd.pg_num"] == "" {
		d.config["ceph.osd.pg_num"] = "32"
	}

	return nil
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (d *ceph) Create() error {
	revert := revert.New()
	defer revert.Fail()

	d.config["volatile.initial_source"] = d.config["source"]

	err := d.FillConfig()
	if err != nil {
		return err
	}

	// Validate.
	_, err = units.ParseByteSizeString(d.config["ceph.osd.pg_num"])
	if err != nil {
		return err
	}

	// Quick check.
	if d.config["source"] != "" && d.config["ceph.osd.pool_name"] != "" && d.config["source"] != d.config["ceph.osd.pool_name"] {
		return fmt.Errorf(`The "source" and "ceph.osd.pool_name" property must not differ for Ceph OSD storage pools`)
	}

	// Use an existing OSD pool.
	if d.config["source"] != "" {
		d.config["ceph.osd.pool_name"] = d.config["source"]
	}

	if d.config["ceph.osd.pool_name"] == "" {
		d.config["ceph.osd.pool_name"] = d.name
		d.config["source"] = d.name
	}

	placeholderVol := d.getPlaceholderVolume()
	poolExists, err := d.osdPoolExists()
	if err != nil {
		return fmt.Errorf("Failed checking the existence of the ceph %q osd pool while attempting to create it because of an internal error: %w", d.config["ceph.osd.pool_name"], err)
	}

	if !poolExists {
		// Create new osd pool.
		_, err := subprocess.TryRunCommand("ceph",
			"--name", fmt.Sprintf("client.%s", d.config["ceph.user.name"]),
			"--cluster", d.config["ceph.cluster_name"],
			"osd",
			"pool",
			"create",
			d.config["ceph.osd.pool_name"],
			d.config["ceph.osd.pg_num"])
		if err != nil {
			return err
		}

		revert.Add(func() { _ = d.osdDeletePool() })

		// Initialize the pool. This is not necessary but allows the pool to be monitored.
		_, err = subprocess.TryRunCommand("rbd",
			"--id", d.config["ceph.user.name"],
			"--cluster", d.config["ceph.cluster_name"],
			"pool",
			"init",
			d.config["ceph.osd.pool_name"])
		if err != nil {
			d.logger.Warn("Failed to initialize pool", logger.Ctx{"pool": d.config["ceph.osd.pool_name"], "cluster": d.config["ceph.cluster_name"]})
		}

		// Create placeholder storage volume. Other instances will use this to detect whether this osd
		// pool is already in use by another instance.
		err = d.rbdCreateVolume(placeholderVol, "0")
		if err != nil {
			return err
		}

		d.config["volatile.pool.pristine"] = "true"
	} else {
		volExists, err := d.HasVolume(placeholderVol)
		if err != nil {
			return err
		}

		if volExists {
			// ceph.osd.force_reuse is deprecated and should not be used. OSD pools are a logical
			// construct there is no good reason not to create one for dedicated use by the daemon.
			if util.IsFalseOrEmpty(d.config["ceph.osd.force_reuse"]) {
				return fmt.Errorf("Pool '%s' in cluster '%s' seems to be in use by another Incus instance. Use 'ceph.osd.force_reuse=true' to force", d.config["ceph.osd.pool_name"], d.config["ceph.cluster_name"])
			}

			d.config["volatile.pool.pristine"] = "false"
		} else {
			// Create placeholder storage volume. Other instances will use this to detect whether this osd
			// pool is already in use by another instance.
			err := d.rbdCreateVolume(placeholderVol, "0")
			if err != nil {
				return err
			}

			d.config["volatile.pool.pristine"] = "true"
		}

		// Use existing OSD pool.
		msg, err := subprocess.RunCommand("ceph",
			"--name", fmt.Sprintf("client.%s", d.config["ceph.user.name"]),
			"--cluster", d.config["ceph.cluster_name"],
			"osd",
			"pool",
			"get",
			d.config["ceph.osd.pool_name"],
			"pg_num")
		if err != nil {
			return err
		}

		idx := strings.Index(msg, "pg_num:")
		if idx == -1 {
			return fmt.Errorf("Failed to parse number of placement groups for pool: %s", msg)
		}

		msg = msg[(idx + len("pg_num:")):]
		msg = strings.TrimSpace(msg)

		// It is ok to update the pool configuration since storage pool
		// creation via API is implemented such that the storage pool is
		// checked for a changed config after this function returns and
		// if so the db for it is updated.
		d.config["ceph.osd.pg_num"] = msg
	}

	revert.Success()

	return nil
}

// Delete removes the storage pool from the storage device.
func (d *ceph) Delete(op *operations.Operation) error {
	// Test if the pool exists.
	poolExists, err := d.osdPoolExists()
	if err != nil {
		return fmt.Errorf("Failed checking the existence of the ceph %q osd pool while attempting to delete it because of an internal error: %w", d.config["ceph.osd.pool_name"], err)
	}

	if !poolExists {
		d.logger.Warn("Pool does not exist", logger.Ctx{"pool": d.config["ceph.osd.pool_name"], "cluster": d.config["ceph.cluster_name"]})
	}

	// Check whether we own the pool and only remove in this case.
	if util.IsTrue(d.config["volatile.pool.pristine"]) {
		// Delete the osd pool.
		if poolExists {
			err := d.osdDeletePool()
			if err != nil {
				return err
			}
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

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (d *ceph) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"ceph.cluster_name":       validate.IsAny,
		"ceph.osd.force_reuse":    validate.Optional(validate.IsBool), // Deprecated, should not be used.
		"ceph.osd.pg_num":         validate.IsAny,
		"ceph.osd.pool_name":      validate.IsAny,
		"ceph.osd.data_pool_name": validate.IsAny,
		"ceph.rbd.clone_copy":     validate.Optional(validate.IsBool),
		"ceph.rbd.du":             validate.Optional(validate.IsBool),
		"ceph.rbd.features":       validate.IsAny,
		"ceph.user.name":          validate.IsAny,
		"volatile.pool.pristine":  validate.IsAny,
	}

	return d.validatePool(config, rules, d.commonVolumeRules())
}

// Update applies any driver changes required from a configuration change.
func (d *ceph) Update(changedConfig map[string]string) error {
	return nil
}

// Mount mounts the storage pool.
func (d *ceph) Mount() (bool, error) {
	placeholderVol := d.getPlaceholderVolume()
	volExists, err := d.HasVolume(placeholderVol)
	if err != nil {
		return false, err
	}

	if !volExists {
		return false, fmt.Errorf("Placeholder volume does not exist")
	}

	return true, nil
}

// Unmount unmounts the storage pool.
func (d *ceph) Unmount() (bool, error) {
	// Nothing to do here.
	return true, nil
}

// GetResources returns the pool resource usage information.
func (d *ceph) GetResources() (*api.ResourcesStoragePool, error) {
	var stdout bytes.Buffer

	err := subprocess.RunCommandWithFds(context.TODO(), nil, &stdout,
		"ceph",
		"--name", fmt.Sprintf("client.%s", d.config["ceph.user.name"]),
		"--cluster", d.config["ceph.cluster_name"],
		"df",
		"-f", "json")
	if err != nil {
		return nil, err
	}

	// Temporary structs for parsing.
	type cephDfPoolStats struct {
		BytesUsed      int64 `json:"bytes_used"`
		BytesAvailable int64 `json:"max_avail"`
	}

	type cephDfPool struct {
		Name  string          `json:"name"`
		Stats cephDfPoolStats `json:"stats"`
	}

	type cephDf struct {
		Pools []cephDfPool `json:"pools"`
	}

	// Parse the JSON output.
	df := cephDf{}
	err = json.NewDecoder(&stdout).Decode(&df)
	if err != nil {
		return nil, err
	}

	var pool *cephDfPool
	for _, entry := range df.Pools {
		if entry.Name == d.config["ceph.osd.pool_name"] {
			pool = &entry
			break
		}
	}

	if pool == nil {
		return nil, fmt.Errorf("OSD pool missing in df output")
	}

	spaceUsed := uint64(pool.Stats.BytesUsed)
	spaceAvailable := uint64(pool.Stats.BytesAvailable)

	res := api.ResourcesStoragePool{}
	res.Space.Total = spaceAvailable + spaceUsed
	res.Space.Used = spaceUsed

	return &res, nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools in preference order.
func (d *ceph) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []localMigration.Type {
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if util.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if refresh {
		var transportType migration.MigrationFSType

		if IsContentBlock(contentType) {
			transportType = migration.MigrationFSType_BLOCK_AND_RSYNC
		} else {
			transportType = migration.MigrationFSType_RSYNC
		}

		return []localMigration.Type{
			{
				FSType:   transportType,
				Features: rsyncFeatures,
			},
		}
	}

	if contentType == ContentTypeBlock {
		return []localMigration.Type{
			{
				FSType: migration.MigrationFSType_RBD,
			},
			{
				FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
				Features: rsyncFeatures,
			},
		}
	}

	return []localMigration.Type{
		{
			FSType: migration.MigrationFSType_RBD,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
