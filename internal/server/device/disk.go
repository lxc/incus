package device

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/cgroup"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/warningtype"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/project"
	storagePools "github.com/lxc/incus/internal/server/storage"
	storageDrivers "github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/internal/server/warnings"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// Special disk "source" value used for generating a VM cloud-init config ISO.
const diskSourceCloudInit = "cloud-init:config"

// Special disk "source" value used for generating a VM agent ISO.
const diskSourceAgent = "agent:config"

// DiskVirtiofsdSockMountOpt indicates the mount option prefix used to provide the virtiofsd socket path to
// the QEMU driver.
const DiskVirtiofsdSockMountOpt = "virtiofsdSock"

// DiskFileDescriptorMountPrefix indicates the mount dev path is using a file descriptor rather than a normal path.
// The Mount.DevPath field will be expected to be in the format: "fd:<fdNum>:<devPath>".
// It still includes the original dev path so that the instance driver can perform additional probing of the path
// to ascertain additional information if needed. However it will not be used to actually pass the path into the
// instance.
const DiskFileDescriptorMountPrefix = "fd"

// DiskDirectIO is used to indicate disk should use direct I/O.
const DiskDirectIO = "directio"

// DiskIOUring is used to indicate disk should use io_uring if the system supports it.
const DiskIOUring = "io_uring"

// DiskLoopBacked is used to indicate disk is backed onto a loop device.
const DiskLoopBacked = "loop"

type diskBlockLimit struct {
	readBps   int64
	readIops  int64
	writeBps  int64
	writeIops int64
}

// diskSourceNotFoundError error used to indicate source not found.
type diskSourceNotFoundError struct {
	msg string
	err error
}

func (e diskSourceNotFoundError) Error() string {
	return fmt.Sprintf("%s: %v", e.msg, e.err)
}

func (e diskSourceNotFoundError) Unwrap() error {
	return e.err
}

type disk struct {
	deviceCommon

	restrictedParentSourcePath string
	pool                       storagePools.Pool
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *disk) CanMigrate() bool {
	// Root disk is always migratable.
	if d.config["path"] == "/" {
		return true
	}

	// Remote disks are migratable.
	if d.pool.Driver().Info().Remote {
		return true
	}

	return false
}

// sourceIsCephFs returns true if the disks source config setting is a CephFS share.
func (d *disk) sourceIsCephFs() bool {
	return strings.HasPrefix(d.config["source"], "cephfs:")
}

// sourceIsCeph returns true if the disks source config setting is a Ceph RBD.
func (d *disk) sourceIsCeph() bool {
	return strings.HasPrefix(d.config["source"], "ceph:")
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *disk) CanHotPlug() bool {
	// Containers support hot-plugging all disk types.
	if d.inst.Type() == instancetype.Container {
		return true
	}

	// Only VirtioFS works with path hotplug.
	// As migration.stateful turns off VirtioFS, this also turns off hotplugging of paths.
	if util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return false
	}

	// Block disks can be hot-plugged into VMs.
	return true
}

// validateConfig checks the supplied config for correctness.
// isRequired indicates whether the supplied device config requires this device to start OK.
func (d *disk) isRequired(devConfig deviceConfig.Device) bool {
	// Defaults to required.
	if util.IsTrueOrEmpty(devConfig["required"]) && util.IsFalseOrEmpty(devConfig["optional"]) {
		return true
	}

	return false
}

// sourceIsLocalPath returns true if the source supplied should be considered a local path on the host.
// It returns false if the disk source is empty, a VM cloud-init config drive, or a remote ceph/cephfs path.
func (d *disk) sourceIsLocalPath(source string) bool {
	if source == "" {
		return false
	}

	if source == diskSourceCloudInit {
		return false
	}

	if source == diskSourceAgent {
		return false
	}

	if d.sourceIsCeph() || d.sourceIsCephFs() {
		return false
	}

	return true
}

// validateConfig checks the supplied config for correctness.
func (d *disk) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	// Supported propagation types.
	// If an empty value is supplied the default behavior is to assume "private" mode.
	// These come from https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt
	propagationTypes := []string{"", "private", "shared", "slave", "unbindable", "rshared", "rslave", "runbindable", "rprivate"}
	validatePropagation := func(input string) error {
		if !slices.Contains(propagationTypes, d.config["bind"]) {
			return fmt.Errorf("Invalid propagation value. Must be one of: %s", strings.Join(propagationTypes, ", "))
		}

		return nil
	}

	rules := map[string]func(string) error{
		"required":          validate.Optional(validate.IsBool),
		"optional":          validate.Optional(validate.IsBool), // "optional" is deprecated, replaced by "required".
		"readonly":          validate.Optional(validate.IsBool),
		"recursive":         validate.Optional(validate.IsBool),
		"shift":             validate.Optional(validate.IsBool),
		"source":            validate.IsAny,
		"limits.read":       validate.IsAny,
		"limits.write":      validate.IsAny,
		"limits.max":        validate.IsAny,
		"size":              validate.Optional(validate.IsSize),
		"size.state":        validate.Optional(validate.IsSize),
		"pool":              validate.IsAny,
		"propagation":       validatePropagation,
		"raw.mount.options": validate.IsAny,
		"ceph.cluster_name": validate.IsAny,
		"ceph.user_name":    validate.IsAny,
		"boot.priority":     validate.Optional(validate.IsUint32),
		"path":              validate.IsAny,
		"io.cache":          validate.Optional(validate.IsOneOf("none", "writeback", "unsafe")),
		"io.bus":            validate.Optional(validate.IsOneOf("nvme", "virtio-blk", "virtio-scsi")),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if instConf.Type() == instancetype.Container && d.config["io.bus"] != "" {
		return fmt.Errorf("IO bus configuration cannot be applied to containers")
	}

	if instConf.Type() == instancetype.Container && d.config["io.cache"] != "" {
		return fmt.Errorf("IO cache configuration cannot be applied to containers")
	}

	if d.config["required"] != "" && d.config["optional"] != "" {
		return fmt.Errorf(`Cannot use both "required" and deprecated "optional" properties at the same time`)
	}

	if d.config["source"] == "" && d.config["path"] != "/" {
		return fmt.Errorf(`Disk entry is missing the required "source" or "path" property`)
	}

	if d.config["path"] == "/" && d.config["source"] != "" {
		return fmt.Errorf(`Root disk entry may not have a "source" property set`)
	}

	if d.config["path"] == "/" && d.config["pool"] == "" {
		return fmt.Errorf(`Root disk entry must have a "pool" property set`)
	}

	if d.config["size"] != "" && d.config["path"] != "/" {
		return fmt.Errorf("Only the root disk may have a size quota")
	}

	if d.config["size.state"] != "" && d.config["path"] != "/" {
		return fmt.Errorf("Only the root disk may have a migration size quota")
	}

	if d.config["recursive"] != "" && (d.config["path"] == "/" || !internalUtil.IsDir(d.config["source"])) {
		return fmt.Errorf("The recursive option is only supported for additional bind-mounted paths")
	}

	if util.IsTrue(d.config["recursive"]) && util.IsTrue(d.config["readonly"]) {
		return fmt.Errorf("Recursive read-only bind-mounts aren't currently supported by the kernel")
	}

	// Check ceph options are only used when ceph or cephfs type source is specified.
	if !(d.sourceIsCeph() || d.sourceIsCephFs()) && (d.config["ceph.cluster_name"] != "" || d.config["ceph.user_name"] != "") {
		return fmt.Errorf("Invalid options ceph.cluster_name/ceph.user_name for source %q", d.config["source"])
	}

	// Check no other devices also have the same path as us. Use LocalDevices for this check so
	// that we can check before the config is expanded or when a profile is being checked.
	// Don't take into account the device names, only count active devices that point to the
	// same path, so that if merged profiles share the same the path and then one is removed
	// this can still be cleanly removed.
	pathCount := 0
	for _, devConfig := range instConf.LocalDevices() {
		if devConfig["type"] == "disk" && d.config["path"] != "" && devConfig["path"] == d.config["path"] {
			pathCount++
			if pathCount > 1 {
				return fmt.Errorf("More than one disk device uses the same path %q", d.config["path"])
			}
		}
	}

	srcPathIsLocal := d.config["pool"] == "" && d.sourceIsLocalPath(d.config["source"])
	srcPathIsAbs := filepath.IsAbs(d.config["source"])

	if srcPathIsLocal && !srcPathIsAbs {
		return fmt.Errorf("Source path must be absolute for local sources")
	}

	// Check that external disk source path exists. External disk sources have a non-empty "source" property
	// that contains the path of the external source, and do not have a "pool" property. We only check the
	// source path exists when the disk device is required, is not an external ceph/cephfs source and is not a
	// VM cloud-init drive. We only check this when an instance is loaded to avoid validating snapshot configs
	// that may contain older config that no longer exists which can prevent migrations.
	if d.inst != nil && srcPathIsLocal && d.isRequired(d.config) && !util.PathExists(d.config["source"]) {
		return fmt.Errorf("Missing source path %q for disk %q", d.config["source"], d.name)
	}

	if d.config["pool"] != "" {
		if d.config["shift"] != "" {
			return fmt.Errorf(`The "shift" property cannot be used with custom storage volumes (set "security.shifted=true" on the volume instead)`)
		}

		if srcPathIsAbs {
			return fmt.Errorf("Storage volumes cannot be specified as absolute paths")
		}

		var dbVolume *db.StorageVolume
		var storageProjectName string

		if d.inst != nil && !d.inst.IsSnapshot() && d.config["source"] != "" && d.config["path"] != "/" {
			d.pool, err = storagePools.LoadByName(d.state, d.config["pool"])
			if err != nil {
				return fmt.Errorf("Failed to get storage pool %q: %w", d.config["pool"], err)
			}

			// Derive the effective storage project name from the instance config's project.
			storageProjectName, err = project.StorageVolumeProject(d.state.DB.Cluster, instConf.Project().Name, db.StoragePoolVolumeTypeCustom)
			if err != nil {
				return err
			}

			// GetStoragePoolVolume returns a volume with an empty Location field for remote drivers.
			err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, d.config["source"], true)
				return err
			})
			if err != nil {
				return fmt.Errorf("Failed loading custom volume: %w", err)
			}

			// Check that block volumes are *only* attached to VM instances.
			contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVolume.ContentType)
			if err != nil {
				return err
			}

			// Check that only shared custom storage block volume are added to profiles, or multiple instances.
			if util.IsFalseOrEmpty(dbVolume.Config["security.shared"]) && contentType == db.StoragePoolVolumeContentTypeBlock {
				if instConf.Type() == instancetype.Any {
					return fmt.Errorf("Cannot add un-shared custom storage block volume to profile")
				}

				var usedBy []string

				err = storagePools.VolumeUsedByInstanceDevices(d.state, d.pool.Name(), storageProjectName, &dbVolume.StorageVolume, true, func(inst db.InstanceArgs, project api.Project, usedByDevices []string) error {
					usedBy = append(usedBy, inst.Name)

					return nil
				})
				if err != nil {
					return err
				}

				if len(usedBy) > 0 {
					return fmt.Errorf("Cannot add un-shared custom storage block volume to more than one instance")
				}
			}
		}

		// Only perform expensive instance pool volume checks when not validating a profile and after
		// device expansion has occurred (to avoid doing it twice during instance load).
		if d.inst != nil && !d.inst.IsSnapshot() && len(instConf.ExpandedDevices()) > 0 {
			if d.pool == nil {
				d.pool, err = storagePools.LoadByName(d.state, d.config["pool"])
				if err != nil {
					return fmt.Errorf("Failed to get storage pool %q: %w", d.config["pool"], err)
				}
			}

			if d.pool.Status() == "Pending" {
				return fmt.Errorf("Pool %q is pending", d.config["pool"])
			}

			// Custom volume validation.
			if d.config["source"] != "" && d.config["path"] != "/" {
				if storageProjectName == "" {
					// Derive the effective storage project name from the instance config's project.
					storageProjectName, err = project.StorageVolumeProject(d.state.DB.Cluster, instConf.Project().Name, db.StoragePoolVolumeTypeCustom)
					if err != nil {
						return err
					}
				}

				if dbVolume == nil {
					// GetStoragePoolVolume returns a volume with an empty Location field for remote drivers.
					err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
						dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, d.config["source"], true)
						return err
					})
					if err != nil {
						return fmt.Errorf("Failed loading custom volume: %w", err)
					}
				}

				// Check storage volume is available to mount on this cluster member.
				remoteInstance, err := storagePools.VolumeUsedByExclusiveRemoteInstancesWithProfiles(d.state, d.config["pool"], storageProjectName, &dbVolume.StorageVolume)
				if err != nil {
					return fmt.Errorf("Failed checking if custom volume is exclusively attached to another instance: %w", err)
				}

				if remoteInstance != nil {
					return fmt.Errorf("Custom volume is already attached to an instance on a different node")
				}

				// Check that block volumes are *only* attached to VM instances.
				contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVolume.ContentType)
				if err != nil {
					return err
				}

				if contentType == db.StoragePoolVolumeContentTypeBlock {
					if instConf.Type() == instancetype.Container {
						return fmt.Errorf("Custom block volumes cannot be used on containers")
					}

					if d.config["path"] != "" {
						return fmt.Errorf("Custom block volumes cannot have a path defined")
					}
				} else if contentType == db.StoragePoolVolumeContentTypeISO {
					if instConf.Type() == instancetype.Container {
						return fmt.Errorf("Custom ISO volumes cannot be used on containers")
					}

					if d.config["path"] != "" {
						return fmt.Errorf("Custom ISO volumes cannot have a path defined")
					}
				} else if d.config["path"] == "" {
					return fmt.Errorf("Custom filesystem volumes require a path to be defined")
				}
			}

			// Extract initial configuration from the profile and validate them against appropriate
			// storage driver. Currently initial configuration is only applicable to root disk devices.
			initialConfig := make(map[string]string)
			for k, v := range d.config {
				prefix, newKey, found := strings.Cut(k, "initial.")
				if found && prefix == "" {
					initialConfig[newKey] = v
				}
			}

			if len(initialConfig) > 0 {
				if !internalInstance.IsRootDiskDevice(d.config) {
					return fmt.Errorf("Non-root disk device cannot contain initial.* configuration")
				}

				volumeType, err := storagePools.InstanceTypeToVolumeType(d.inst.Type())
				if err != nil {
					return err
				}

				// Create temporary volume definition.
				vol := storageDrivers.NewVolume(
					d.pool.Driver(),
					d.pool.Name(),
					volumeType,
					storagePools.InstanceContentType(d.inst),
					d.name,
					initialConfig,
					d.pool.Driver().Config())

				err = d.pool.Driver().ValidateVolume(vol, true)
				if err != nil {
					return fmt.Errorf("Invalid initial device configuration: %v", err)
				}
			}
		}
	}

	return nil
}

// getDevicePath returns the absolute path on the host for this instance and supplied device config.
func (d *disk) getDevicePath(devName string, devConfig deviceConfig.Device) string {
	relativeDestPath := strings.TrimPrefix(devConfig["path"], "/")
	devPath := linux.PathNameEncode(deviceJoinPath("disk", devName, relativeDestPath))
	return filepath.Join(d.inst.DevicesPath(), devPath)
}

// validateEnvironmentSourcePath checks the source path property is valid and allowed by project.
func (d *disk) validateEnvironmentSourcePath() error {
	srcPathIsLocal := d.config["pool"] == "" && d.sourceIsLocalPath(d.config["source"])
	if !srcPathIsLocal {
		return nil
	}

	sourceHostPath := d.config["source"]

	// Check local external disk source path exists, but don't follow symlinks here (as we let openat2 do that
	// safely later).
	_, err := os.Lstat(sourceHostPath)
	if err != nil {
		if os.IsNotExist(err) {
			return diskSourceNotFoundError{msg: fmt.Sprintf("Missing source path %q", d.config["source"])}
		}

		return fmt.Errorf("Failed accessing source path %q for disk %q: %w", sourceHostPath, d.name, err)
	}

	// If project not default then check if using restricted disk paths.
	// Default project cannot be restricted, so don't bother loading the project config in that case.
	instProject := d.inst.Project()
	if instProject.Name != api.ProjectDefaultName {
		// If restricted disk paths are in force, then check the disk's source is allowed, and record the
		// allowed parent path for later user during device start up sequence.
		if util.IsTrue(instProject.Config["restricted"]) && instProject.Config["restricted.devices.disk.paths"] != "" {
			allowed, restrictedParentSourcePath := project.CheckRestrictedDevicesDiskPaths(instProject.Config, d.config["source"])
			if !allowed {
				return fmt.Errorf("Disk source path %q not allowed by project for disk %q", d.config["source"], d.name)
			}

			if util.IsTrue(d.config["shift"]) {
				return fmt.Errorf(`The "shift" property cannot be used with a restricted source path`)
			}

			d.restrictedParentSourcePath = restrictedParentSourcePath
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *disk) validateEnvironment() error {
	if d.inst.Type() != instancetype.VM && slices.Contains([]string{diskSourceCloudInit, diskSourceAgent}, d.config["source"]) {
		return fmt.Errorf("disks with source=%s are only supported by virtual machines", d.config["source"])
	}

	err := d.validateEnvironmentSourcePath()
	if err != nil {
		return err
	}

	return nil
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *disk) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*disk)
	if !match {
		return []string{}
	}

	return []string{"limits.max", "limits.read", "limits.write", "size", "size.state"}
}

// Register calls mount for the disk volume (which should already be mounted) to reinitialise the reference counter
// for volumes attached to running instances on daemon restart.
func (d *disk) Register() error {
	d.logger.Debug("Initialising mounted disk ref counter")

	if d.config["path"] == "/" {
		pool, err := storagePools.LoadByInstance(d.state, d.inst)
		if err != nil {
			return err
		}

		// Try to mount the volume that should already be mounted to reinitialise the ref counter.
		_, err = pool.MountInstance(d.inst, nil)
		if err != nil {
			return err
		}
	} else if d.config["path"] != "/" && d.config["source"] != "" && d.config["pool"] != "" {
		storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		// Try to mount the volume that should already be mounted to reinitialise the ref counter.
		_, err = d.pool.MountCustomVolume(storageProjectName, d.config["source"], nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// PreStartCheck checks the storage pool is available (if relevant).
func (d *disk) PreStartCheck() error {
	// Non-pool disks are not relevant for checking pool availability.
	if d.pool == nil {
		return nil
	}

	// Custom volume disks that are not required don't need to be checked as if the pool is
	// not available we should still start the instance.
	if d.config["path"] != "/" && util.IsFalse(d.config["required"]) {
		return nil
	}

	// If disk is required and storage pool is not available, don't try and start instance.
	if d.pool.LocalStatus() == api.StoragePoolStatusUnvailable {
		return api.StatusErrorf(http.StatusServiceUnavailable, "Storage pool %q unavailable on this server", d.pool.Name())
	}

	return nil
}

// Start is run when the device is added to the instance.
func (d *disk) Start() (*deviceConfig.RunConfig, error) {
	var runConfig *deviceConfig.RunConfig

	err := d.validateEnvironment()
	if err == nil {
		if d.inst.Type() == instancetype.VM {
			runConfig, err = d.startVM()
		} else {
			runConfig, err = d.startContainer()
		}
	}

	if err != nil {
		var sourceNotFound diskSourceNotFoundError
		if errors.As(err, &sourceNotFound) && !d.isRequired(d.config) {
			d.logger.Warn(sourceNotFound.msg)
			return nil, nil
		}

		return nil, err
	}

	return runConfig, nil
}

// startContainer starts the disk device for a container instance.
func (d *disk) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	isReadOnly := util.IsTrue(d.config["readonly"])

	// Apply cgroups only after all the mounts have been processed.
	runConf.PostHooks = append(runConf.PostHooks, func() error {
		runConf := deviceConfig.RunConfig{}

		err := d.generateLimits(&runConf)
		if err != nil {
			return err
		}

		err = d.inst.DeviceEventHandler(&runConf)
		if err != nil {
			return err
		}

		return nil
	})

	revert := revert.New()
	defer revert.Fail()

	// Deal with a rootfs.
	if internalInstance.IsRootDiskDevice(d.config) {
		// Set the rootfs path.
		rootfs := deviceConfig.RootFSEntryItem{
			Path: d.inst.RootfsPath(),
		}

		// Read-only rootfs (unlikely to work very well).
		if isReadOnly {
			rootfs.Opts = append(rootfs.Opts, "ro")
		}

		// Handle previous requests for setting new quotas.
		err := d.applyDeferredQuota()
		if err != nil {
			return nil, err
		}

		runConf.RootFS = rootfs
	} else {
		// Source path.
		srcPath := d.config["source"]

		// Destination path.
		destPath := d.config["path"]
		relativeDestPath := strings.TrimPrefix(destPath, "/")

		// Option checks.
		isRecursive := util.IsTrue(d.config["recursive"])

		ownerShift := deviceConfig.MountOwnerShiftNone
		if util.IsTrue(d.config["shift"]) {
			ownerShift = deviceConfig.MountOwnerShiftDynamic
		}

		// If ownerShift is none and pool is specified then check whether the pool itself
		// has owner shifting enabled, and if so enable shifting on this device too.
		if ownerShift == deviceConfig.MountOwnerShiftNone && d.config["pool"] != "" {
			// Only custom volumes can be attached currently.
			storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
			if err != nil {
				return nil, err
			}

			var dbVolume *db.StorageVolume
			err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, d.config["source"], true)
				return err
			})
			if err != nil {
				return nil, err
			}

			if util.IsTrue(dbVolume.Config["security.shifted"]) {
				ownerShift = "dynamic"
			}
		}

		options := []string{}
		if isReadOnly {
			options = append(options, "ro")
		}

		if isRecursive {
			options = append(options, "rbind")
		} else {
			options = append(options, "bind")
		}

		if d.config["propagation"] != "" {
			options = append(options, d.config["propagation"])
		}

		// Mount the pool volume and set poolVolSrcPath for createDevice below.
		if d.config["pool"] != "" {
			var err error
			var revertFunc func()
			var mountInfo *storagePools.MountInfo

			revertFunc, srcPath, mountInfo, err = d.mountPoolVolume()
			if err != nil {
				return nil, diskSourceNotFoundError{msg: "Failed mounting volume", err: err}
			}

			revert.Add(revertFunc)

			// Handle post hooks.
			runConf.PostHooks = append(runConf.PostHooks, func() error {
				for _, hook := range mountInfo.PostHooks {
					err := hook(d.inst)
					if err != nil {
						return err
					}
				}

				return nil
			})
		}

		// Mount the source in the instance devices directory.
		revertFunc, sourceDevPath, isFile, err := d.createDevice(srcPath)
		if err != nil {
			return nil, err
		}

		revert.Add(revertFunc)

		if isFile {
			options = append(options, "create=file")
		} else {
			options = append(options, "create=dir")
		}

		// Ask for the mount to be performed.
		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			DevName:    d.name,
			DevPath:    sourceDevPath,
			TargetPath: relativeDestPath,
			FSType:     "none",
			Opts:       options,
			OwnerShift: ownerShift,
		})

		// Unmount host-side mount once instance is started.
		runConf.PostHooks = append(runConf.PostHooks, d.postStart)
	}

	revert.Success()
	return &runConf, nil
}

// vmVirtfsProxyHelperPaths returns the path for PID file to use with virtfs-proxy-helper process.
func (d *disk) vmVirtfsProxyHelperPaths() string {
	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))

	return pidPath
}

// vmVirtiofsdPaths returns the path for the socket and PID file to use with virtiofsd process.
func (d *disk) vmVirtiofsdPaths() (string, string) {
	sockPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("virtio-fs.%s.sock", d.name))
	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("virtio-fs.%s.pid", d.name))

	return sockPath, pidPath
}

func (d *disk) detectVMPoolMountOpts() []string {
	var opts []string

	driverConf := d.pool.Driver().Config()

	// If the pool's source is a normal file, rather than a block device or directory, then we consider it to
	// be a loop backed stored pool.
	fileInfo, _ := os.Stat(driverConf["source"])
	if fileInfo != nil && !linux.IsBlockdev(fileInfo.Mode()) && !fileInfo.IsDir() {
		opts = append(opts, DiskLoopBacked)
	}

	if d.pool.Driver().Info().DirectIO {
		opts = append(opts, DiskDirectIO)
	}

	if d.pool.Driver().Info().IOUring {
		opts = append(opts, DiskIOUring)
	}

	return opts
}

// startVM starts the disk device for a virtual machine instance.
func (d *disk) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}

	revert := revert.New()
	defer revert.Fail()

	// Handle user overrides.
	opts := []string{}

	// Allow the user to override the bus.
	if d.config["io.bus"] != "" {
		opts = append(opts, fmt.Sprintf("bus=%s", d.config["io.bus"]))
	}

	// Allow the user to override the caching mode.
	if d.config["io.cache"] != "" {
		opts = append(opts, fmt.Sprintf("cache=%s", d.config["io.cache"]))
	}

	if internalInstance.IsRootDiskDevice(d.config) {
		// Handle previous requests for setting new quotas.
		err := d.applyDeferredQuota()
		if err != nil {
			return nil, err
		}

		opts = append(opts, d.detectVMPoolMountOpts()...)

		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				TargetPath: d.config["path"], // Indicator used that this is the root device.
				DevName:    d.name,
				Opts:       opts,
			},
		}

		return &runConf, nil
	} else if d.config["source"] == diskSourceAgent {
		// This is a special virtual disk source that can be attached to a VM to provide agent binary and config.
		isoPath, err := d.generateVMAgentDrive()
		if err != nil {
			return nil, err
		}

		// Open file handle to isoPath source.
		f, err := os.OpenFile(isoPath, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("Failed opening source path %q: %w", isoPath, err)
		}

		revert.Add(func() { _ = f.Close() })
		runConf.PostHooks = append(runConf.PostHooks, f.Close)
		runConf.Revert = func() { _ = f.Close() } // Close file on VM start failure.

		// Encode the file descriptor and original isoPath into the DevPath field.
		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				DevPath: fmt.Sprintf("%s:%d:%s", DiskFileDescriptorMountPrefix, f.Fd(), isoPath),
				DevName: d.name,
				FSType:  "iso9660",
				Opts:    opts,
			},
		}

		revert.Success()
		return &runConf, nil
	} else if d.config["source"] == diskSourceCloudInit {
		// This is a special virtual disk source that can be attached to a VM to provide cloud-init config.
		isoPath, err := d.generateVMConfigDrive()
		if err != nil {
			return nil, err
		}

		// Open file handle to isoPath source.
		f, err := os.OpenFile(isoPath, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("Failed opening source path %q: %w", isoPath, err)
		}

		revert.Add(func() { _ = f.Close() })
		runConf.PostHooks = append(runConf.PostHooks, f.Close)
		runConf.Revert = func() { _ = f.Close() } // Close file on VM start failure.

		// Encode the file descriptor and original isoPath into the DevPath field.
		runConf.Mounts = []deviceConfig.MountEntryItem{
			{
				DevPath: fmt.Sprintf("%s:%d:%s", DiskFileDescriptorMountPrefix, f.Fd(), isoPath),
				DevName: d.name,
				FSType:  "iso9660",
				Opts:    opts,
			},
		}

		revert.Success()
		return &runConf, nil
	} else if d.config["source"] != "" {
		if d.sourceIsCeph() {
			// Get the pool and volume names.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			clusterName, userName := d.cephCreds()
			runConf.Mounts = []deviceConfig.MountEntryItem{
				{
					DevPath: DiskGetRBDFormat(clusterName, userName, fields[0], fields[1]),
					DevName: d.name,
					Opts:    opts,
				},
			}
		} else {
			var err error

			// Default to block device or image file passthrough first.
			mount := deviceConfig.MountEntryItem{
				DevPath: d.config["source"],
				DevName: d.name,
				Opts:    opts,
			}

			// Mount the pool volume and update srcPath to mount path so it can be recognised as dir
			// if the volume is a filesystem volume type (if it is a block volume the srcPath will
			// be returned as the path to the block device).
			if d.config["pool"] != "" {
				var revertFunc func()

				// Derive the effective storage project name from the instance config's project.
				storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
				if err != nil {
					return nil, err
				}

				// GetStoragePoolVolume returns a volume with an empty Location field for remote drivers.
				var dbVolume *db.StorageVolume
				err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, d.config["source"], true)
					return err
				})
				if err != nil {
					return nil, fmt.Errorf("Failed loading custom volume: %w", err)
				}

				contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVolume.ContentType)
				if err != nil {
					return nil, err
				}

				if contentType == db.StoragePoolVolumeContentTypeISO {
					mount.FSType = "iso9660"
				}

				// If the pool is ceph backed and a block device, don't mount it, instead pass config to QEMU instance
				// to use the built in RBD support.
				if d.pool.Driver().Info().Name == "ceph" && (contentType == db.StoragePoolVolumeContentTypeBlock || contentType == db.StoragePoolVolumeContentTypeISO) {
					config := d.pool.ToAPI().Config
					poolName := config["ceph.osd.pool_name"]

					userName := config["ceph.user.name"]
					if userName == "" {
						userName = storageDrivers.CephDefaultUser
					}

					clusterName := config["ceph.cluster_name"]
					if clusterName == "" {
						clusterName = storageDrivers.CephDefaultUser
					}

					mount := deviceConfig.MountEntryItem{
						DevPath: DiskGetRBDFormat(clusterName, userName, poolName, d.config["source"]),
						DevName: d.name,
						Opts:    opts,
					}

					if contentType == db.StoragePoolVolumeContentTypeISO {
						mount.FSType = "iso9660"
					}

					runConf.Mounts = []deviceConfig.MountEntryItem{mount}

					return &runConf, nil
				}

				revertFunc, mount.DevPath, _, err = d.mountPoolVolume()
				if err != nil {
					return nil, diskSourceNotFoundError{msg: "Failed mounting volume", err: err}
				}

				revert.Add(revertFunc)

				mount.Opts = append(mount.Opts, d.detectVMPoolMountOpts()...)
			}

			if util.IsTrue(d.config["readonly"]) {
				mount.Opts = append(mount.Opts, "ro")
			}

			// If the source being added is a directory or cephfs share, then we will use the agent
			// directory sharing feature to mount the directory inside the VM, and as such we need to
			// indicate to the VM the target path to mount to.
			if internalUtil.IsDir(mount.DevPath) || d.sourceIsCephFs() {
				if d.config["path"] == "" {
					return nil, fmt.Errorf(`Missing mount "path" setting`)
				}

				// Mount the source in the instance devices directory.
				// This will ensure that if the exported directory configured as readonly that this
				// takes effect event if using virtio-fs (which doesn't support read only mode) by
				// having the underlying mount setup as readonly.
				var revertFunc func()
				revertFunc, mount.DevPath, _, err = d.createDevice(mount.DevPath)
				if err != nil {
					return nil, err
				}

				revert.Add(revertFunc)

				mount.TargetPath = d.config["path"]
				mount.FSType = "9p"

				rawIDMaps, err := idmap.NewSetFromIncusIDMap(d.inst.ExpandedConfig()["raw.idmap"])
				if err != nil {
					return nil, fmt.Errorf(`Failed parsing instance "raw.idmap": %w`, err)
				}

				// If we are using restricted parent source path mode, or if a non-empty set of
				// raw ID maps have been supplied, then we will be running the disk proxy processes
				// inside a user namespace as the root userns user. Therefore we need to ensure
				// that there is a root UID and GID mapping in the raw ID maps, and if not then add
				// one mapping the root userns user to the nouser/nogroup host ID.
				if d.restrictedParentSourcePath != "" || len(rawIDMaps.Entries) > 0 {
					rawIDMaps.Entries = diskAddRootUserNSEntry(rawIDMaps.Entries, 65534)
				}

				// Start virtiofsd for virtio-fs share. The agent prefers to use this over the
				// virtfs-proxy-helper 9p share. The 9p share will only be used as a fallback.
				err = func() error {
					sockPath, pidPath := d.vmVirtiofsdPaths()
					logPath := filepath.Join(d.inst.LogPath(), fmt.Sprintf("disk.%s.log", d.name))
					_ = os.Remove(logPath) // Remove old log if needed.

					revertFunc, unixListener, err := DiskVMVirtiofsdStart(d.state.OS.ExecPath, d.inst, sockPath, pidPath, logPath, mount.DevPath, rawIDMaps.Entries)
					if err != nil {
						var errUnsupported UnsupportedError
						if errors.As(err, &errUnsupported) {
							d.logger.Warn("Unable to use virtio-fs for device, using 9p as a fallback", logger.Ctx{"err": errUnsupported})

							if errUnsupported == ErrMissingVirtiofsd {
								_ = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
									return tx.UpsertWarningLocalNode(ctx, d.inst.Project().Name, cluster.TypeInstance, d.inst.ID(), warningtype.MissingVirtiofsd, "Using 9p as a fallback")
								})
							} else {
								// Resolve previous warning.
								_ = warnings.ResolveWarningsByLocalNodeAndProjectAndType(d.state.DB.Cluster, d.inst.Project().Name, warningtype.MissingVirtiofsd)
							}

							return nil
						}

						return err
					}

					revert.Add(revertFunc)

					// Request the unix listener is closed after QEMU has connected on startup.
					runConf.PostHooks = append(runConf.PostHooks, unixListener.Close)

					// Resolve previous warning
					_ = warnings.ResolveWarningsByLocalNodeAndProjectAndType(d.state.DB.Cluster, d.inst.Project().Name, warningtype.MissingVirtiofsd)

					// Add the socket path to the mount options to indicate to the qemu driver
					// that this share is available.
					// Note: the sockPath is not passed to the QEMU via mount.DevPath like the
					// 9p share above. This is because we run the 9p share concurrently
					// and can only pass one DevPath at a time. Instead pass the sock path to
					// the QEMU driver via the mount opts field as virtiofsdSock to allow the
					// QEMU driver also setup the virtio-fs share.
					mount.Opts = append(mount.Opts, fmt.Sprintf("%s=%s", DiskVirtiofsdSockMountOpt, sockPath))

					return nil
				}()
				if err != nil {
					return nil, fmt.Errorf("Failed to setup virtiofsd for device %q: %w", d.name, err)
				}

				// We can't hotplug 9p shares, so only do 9p for stopped instances.
				if !d.inst.IsRunning() {
					// Start virtfs-proxy-helper for 9p share (this will rewrite mount.DevPath with
					// socket FD number so must come after starting virtiofsd).
					err = func() error {
						sockFile, cleanup, err := DiskVMVirtfsProxyStart(d.state.OS.ExecPath, d.vmVirtfsProxyHelperPaths(), mount.DevPath, rawIDMaps.Entries)
						if err != nil {
							return err
						}

						revert.Add(cleanup)

						// Request the unix socket is closed after QEMU has connected on startup.
						runConf.PostHooks = append(runConf.PostHooks, sockFile.Close)

						// Use 9p socket FD number as dev path so qemu can connect to the proxy.
						mount.DevPath = fmt.Sprintf("%d", sockFile.Fd())

						return nil
					}()
					if err != nil {
						return nil, fmt.Errorf("Failed to setup virtfs-proxy-helper for device %q: %w", d.name, err)
					}
				}
			} else {
				f, err := d.localSourceOpen(mount.DevPath)
				if err != nil {
					return nil, err
				}

				revert.Add(func() { _ = f.Close() })
				runConf.PostHooks = append(runConf.PostHooks, f.Close)
				runConf.Revert = func() { _ = f.Close() } // Close file on VM start failure.

				// Detect ISO files to set correct FSType before DevPath is encoded below.
				// This is very important to support Windows ISO images (amongst other).
				if strings.HasSuffix(mount.DevPath, ".iso") {
					mount.FSType = "iso9660"
				}

				// Encode the file descriptor and original srcPath into the DevPath field.
				mount.DevPath = fmt.Sprintf("%s:%d:%s", DiskFileDescriptorMountPrefix, f.Fd(), mount.DevPath)
			}

			// Add successfully setup mount config to runConf.
			runConf.Mounts = []deviceConfig.MountEntryItem{mount}
		}

		revert.Success()
		return &runConf, nil
	}

	return nil, fmt.Errorf("Disk type not supported for VMs")
}

// postStart is run after the instance is started.
func (d *disk) postStart() error {
	devPath := d.getDevicePath(d.name, d.config)

	// Unmount the host side.
	err := unix.Unmount(devPath, unix.MNT_DETACH)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *disk) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	if d.inst.Type() == instancetype.VM && !internalInstance.IsRootDiskDevice(d.config) {
		return fmt.Errorf("Non-root disks not supported for VMs")
	}

	if internalInstance.IsRootDiskDevice(d.config) {
		// Make sure we have a valid root disk device (and only one).
		expandedDevices := d.inst.ExpandedDevices()
		newRootDiskDeviceKey, _, err := internalInstance.GetRootDiskDevice(expandedDevices.CloneNative())
		if err != nil {
			return fmt.Errorf("Detect root disk device: %w", err)
		}

		// Retrieve the first old root disk device key, even if there are duplicates.
		oldRootDiskDeviceKey := ""
		for k, v := range oldDevices {
			if internalInstance.IsRootDiskDevice(v) {
				oldRootDiskDeviceKey = k
				break
			}
		}

		// Check for pool change.
		oldRootDiskDevicePool := oldDevices[oldRootDiskDeviceKey]["pool"]
		newRootDiskDevicePool := expandedDevices[newRootDiskDeviceKey]["pool"]
		if oldRootDiskDevicePool != newRootDiskDevicePool {
			return fmt.Errorf("The storage pool of the root disk can only be changed through move")
		}

		// Deal with quota changes.
		oldRootDiskDeviceSize := oldDevices[oldRootDiskDeviceKey]["size"]
		newRootDiskDeviceSize := expandedDevices[newRootDiskDeviceKey]["size"]
		oldRootDiskDeviceMigrationSize := oldDevices[oldRootDiskDeviceKey]["size.state"]
		newRootDiskDeviceMigrationSize := expandedDevices[newRootDiskDeviceKey]["size.state"]

		// Apply disk quota changes.
		if newRootDiskDeviceSize != oldRootDiskDeviceSize || oldRootDiskDeviceMigrationSize != newRootDiskDeviceMigrationSize {
			// Remove any outstanding volatile apply_quota key if applying a new quota.
			v := d.volatileGet()
			if v["apply_quota"] != "" {
				err = d.volatileSet(map[string]string{"apply_quota": ""})
				if err != nil {
					return err
				}
			}

			err := d.applyQuota(false)
			if errors.Is(err, storageDrivers.ErrInUse) {
				// Save volatile apply_quota key for next boot if cannot apply now.
				err = d.volatileSet(map[string]string{"apply_quota": "true"})
				if err != nil {
					return err
				}

				d.logger.Warn("Could not apply quota because disk is in use, deferring until next start")
			} else if err != nil {
				return err
			}
		}
	}

	// Only apply IO limits if instance is container and is running.
	if isRunning && d.inst.Type() == instancetype.Container {
		runConf := deviceConfig.RunConfig{}
		err := d.generateLimits(&runConf)
		if err != nil {
			return err
		}

		err = d.inst.DeviceEventHandler(&runConf)
		if err != nil {
			return err
		}
	}

	return nil
}

// applyDeferredQuota attempts to apply the deferred quota specified in the volatile "apply_quota" key if set.
// If successfully applies new quota then removes the volatile "apply_quota" key.
func (d *disk) applyDeferredQuota() error {
	v := d.volatileGet()
	if v["apply_quota"] != "" {
		d.logger.Info("Applying deferred quota change")

		// Indicate that we want applyQuota to unmount the volume first, this is so we can perform resizes
		// that cannot be done when the volume is in use.
		err := d.applyQuota(true)
		if err != nil {
			return fmt.Errorf("Failed to apply deferred quota from %q: %w", fmt.Sprintf("volatile.%s.apply_quota", d.name), err)
		}

		// Remove volatile apply_quota key if successful.
		err = d.volatileSet(map[string]string{"apply_quota": ""})
		if err != nil {
			return err
		}
	}

	return nil
}

// applyQuota attempts to resize the instance root disk to the specified size.
// If remount is true, attempts to unmount first before resizing and then mounts again afterwards.
func (d *disk) applyQuota(remount bool) error {
	rootDisk, _, err := internalInstance.GetRootDiskDevice(d.inst.ExpandedDevices().CloneNative())
	if err != nil {
		return fmt.Errorf("Detect root disk device: %w", err)
	}

	newSize := d.inst.ExpandedDevices()[rootDisk]["size"]
	newMigrationSize := d.inst.ExpandedDevices()[rootDisk]["size.state"]

	pool, err := storagePools.LoadByInstance(d.state, d.inst)
	if err != nil {
		return err
	}

	if remount {
		err := pool.UnmountInstance(d.inst, nil)
		if err != nil {
			return err
		}
	}

	quotaErr := pool.SetInstanceQuota(d.inst, newSize, newMigrationSize, nil)

	if remount {
		_, err = pool.MountInstance(d.inst, nil)
	}

	// Return quota set error if failed.
	if quotaErr != nil {
		return quotaErr
	}

	// Return remount error if mount failed.
	if err != nil {
		return err
	}

	return nil
}

// generateLimits adds a set of cgroup rules to apply specified limits to the supplied RunConfig.
func (d *disk) generateLimits(runConf *deviceConfig.RunConfig) error {
	// Disk throttle limits.
	hasDiskLimits := false
	for _, dev := range d.inst.ExpandedDevices() {
		if dev["type"] != "disk" {
			continue
		}

		if dev["limits.read"] != "" || dev["limits.write"] != "" || dev["limits.max"] != "" {
			hasDiskLimits = true
		}
	}

	if hasDiskLimits {
		if !d.state.OS.CGInfo.Supports(cgroup.Blkio, nil) {
			return fmt.Errorf("Cannot apply disk limits as blkio cgroup controller is missing")
		}

		diskLimits, err := d.getDiskLimits()
		if err != nil {
			return err
		}

		cg, err := cgroup.New(&cgroupWriter{runConf})
		if err != nil {
			return err
		}

		for block, limit := range diskLimits {
			if limit.readBps > 0 {
				err = cg.SetBlkioLimit(block, "read", "bps", limit.readBps)
				if err != nil {
					return err
				}
			}

			if limit.readIops > 0 {
				err = cg.SetBlkioLimit(block, "read", "iops", limit.readIops)
				if err != nil {
					return err
				}
			}

			if limit.writeBps > 0 {
				err = cg.SetBlkioLimit(block, "write", "bps", limit.writeBps)
				if err != nil {
					return err
				}
			}

			if limit.writeIops > 0 {
				err = cg.SetBlkioLimit(block, "write", "iops", limit.writeIops)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

type cgroupWriter struct {
	runConf *deviceConfig.RunConfig
}

func (w *cgroupWriter) Get(version cgroup.Backend, controller string, key string) (string, error) {
	return "", fmt.Errorf("This cgroup handler does not support reading")
}

func (w *cgroupWriter) Set(version cgroup.Backend, controller string, key string, value string) error {
	w.runConf.CGroups = append(w.runConf.CGroups, deviceConfig.RunConfigItem{
		Key:   key,
		Value: value,
	})

	return nil
}

// mountPoolVolume mounts the pool volume specified in d.config["source"] from pool specified in d.config["pool"]
// and return the mount path and MountInfo struct. If the instance type is container volume will be shifted if needed.
func (d *disk) mountPoolVolume() (func(), string, *storagePools.MountInfo, error) {
	revert := revert.New()
	defer revert.Fail()

	var mountInfo *storagePools.MountInfo

	// Deal with mounting storage volumes created via the storage api. Extract the name of the storage volume
	// that we are supposed to attach. We assume that the only syntactically valid ways of specifying a
	// storage volume are:
	// - <volume_name>
	// - <type>/<volume_name>
	// Currently, <type> must either be empty or "custom".
	// We do not yet support instance mounts.
	if filepath.IsAbs(d.config["source"]) {
		return nil, "", nil, fmt.Errorf(`When the "pool" property is set "source" must specify the name of a volume, not a path`)
	}

	volumeTypeName := ""
	volumeName := filepath.Clean(d.config["source"])
	slash := strings.Index(volumeName, "/")
	if (slash > 0) && (len(volumeName) > slash) {
		// Extract volume name.
		volumeName = d.config["source"][(slash + 1):]
		// Extract volume type.
		volumeTypeName = d.config["source"][:slash]
	}

	var srcPath string

	// Check volume type name is custom.
	switch volumeTypeName {
	case db.StoragePoolVolumeTypeNameContainer:
		return nil, "", nil, fmt.Errorf("Using instance storage volumes is not supported")
	case "":
		// We simply received the name of a storage volume.
		volumeTypeName = db.StoragePoolVolumeTypeNameCustom
		fallthrough
	case db.StoragePoolVolumeTypeNameCustom:
		break
	case db.StoragePoolVolumeTypeNameImage:
		return nil, "", nil, fmt.Errorf("Using image storage volumes is not supported")
	default:
		return nil, "", nil, fmt.Errorf("Unknown storage type prefix %q found", volumeTypeName)
	}

	// Only custom volumes can be attached currently.
	storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return nil, "", nil, err
	}

	volStorageName := project.StorageVolume(storageProjectName, volumeName)
	srcPath = storageDrivers.GetVolumeMountPath(d.config["pool"], storageDrivers.VolumeTypeCustom, volStorageName)

	mountInfo, err = d.pool.MountCustomVolume(storageProjectName, volumeName, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("Failed mounting storage volume %q of type %q on storage pool %q: %w", volumeName, volumeTypeName, d.pool.Name(), err)
	}

	revert.Add(func() { _, _ = d.pool.UnmountCustomVolume(storageProjectName, volumeName, nil) })

	var dbVolume *db.StorageVolume
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, volumeName, true)
		return err
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("Failed to fetch local storage volume record: %w", err)
	}

	if d.inst.Type() == instancetype.Container {
		if dbVolume.ContentType == db.StoragePoolVolumeContentTypeNameFS {
			err = d.storagePoolVolumeAttachShift(storageProjectName, d.pool.Name(), volumeName, db.StoragePoolVolumeTypeCustom, srcPath)
			if err != nil {
				return nil, "", nil, fmt.Errorf("Failed shifting storage volume %q of type %q on storage pool %q: %w", volumeName, volumeTypeName, d.pool.Name(), err)
			}
		} else {
			return nil, "", nil, fmt.Errorf("Only filesystem volumes are supported for containers")
		}
	}

	if dbVolume.ContentType == db.StoragePoolVolumeContentTypeNameBlock || dbVolume.ContentType == db.StoragePoolVolumeContentTypeNameISO {
		srcPath, err = d.pool.GetCustomVolumeDisk(storageProjectName, volumeName)
		if err != nil {
			return nil, "", nil, fmt.Errorf("Failed to get disk path: %w", err)
		}
	}

	cleanup := revert.Clone().Fail // Clone before calling revert.Success() so we can return the Fail func.
	revert.Success()
	return cleanup, srcPath, mountInfo, err
}

// createDevice creates a disk device mount on host.
// The srcPath argument is the source of the disk device on the host.
// Returns the created device path, and whether the path is a file or not.
func (d *disk) createDevice(srcPath string) (func(), string, bool, error) {
	revert := revert.New()
	defer revert.Fail()

	// Paths.
	devPath := d.getDevicePath(d.name, d.config)

	isReadOnly := util.IsTrue(d.config["readonly"])
	isRecursive := util.IsTrue(d.config["recursive"])

	mntOptions := util.SplitNTrimSpace(d.config["raw.mount.options"], ",", -1, true)
	fsName := "none"

	var isFile bool
	if d.config["pool"] == "" {
		if d.sourceIsCephFs() {
			// Get fs name and path from d.config.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			mdsName := fields[0]
			mdsPath := fields[1]
			clusterName, userName := d.cephCreds()

			// Get the mount options.
			mntSrcPath, fsOptions, fsErr := diskCephfsOptions(clusterName, userName, mdsName, mdsPath)
			if fsErr != nil {
				return nil, "", false, fsErr
			}

			// Join the options with any provided by the user.
			mntOptions = append(mntOptions, fsOptions...)

			fsName = "ceph"
			srcPath = mntSrcPath
			isFile = false
		} else if d.sourceIsCeph() {
			// Get the pool and volume names.
			fields := strings.SplitN(d.config["source"], ":", 2)
			fields = strings.SplitN(fields[1], "/", 2)
			poolName := fields[0]
			volumeName := fields[1]
			clusterName, userName := d.cephCreds()

			// Map the RBD.
			rbdPath, err := diskCephRbdMap(clusterName, userName, poolName, volumeName)
			if err != nil {
				return nil, "", false, diskSourceNotFoundError{msg: "Failed mapping Ceph RBD volume", err: err}
			}

			fsName, err = BlockFsDetect(rbdPath)
			if err != nil {
				return nil, "", false, fmt.Errorf("Failed detecting source path %q block device filesystem: %w", rbdPath, err)
			}

			// Record the device path.
			err = d.volatileSet(map[string]string{"ceph_rbd": rbdPath})
			if err != nil {
				return nil, "", false, err
			}

			srcPath = rbdPath
			isFile = false
		} else {
			fileInfo, err := os.Stat(srcPath)
			if err != nil {
				return nil, "", false, fmt.Errorf("Failed accessing source path %q: %w", srcPath, err)
			}

			fileMode := fileInfo.Mode()
			if linux.IsBlockdev(fileMode) {
				fsName, err = BlockFsDetect(srcPath)
				if err != nil {
					return nil, "", false, fmt.Errorf("Failed detecting source path %q block device filesystem: %w", srcPath, err)
				}
			} else if !fileMode.IsDir() {
				isFile = true
			}

			f, err := d.localSourceOpen(srcPath)
			if err != nil {
				return nil, "", false, err
			}

			defer func() { _ = f.Close() }()

			srcPath = fmt.Sprintf("/proc/self/fd/%d", f.Fd())
		}
	}

	// Create the devices directory if missing.
	if !util.PathExists(d.inst.DevicesPath()) {
		err := os.Mkdir(d.inst.DevicesPath(), 0711)
		if err != nil {
			return nil, "", false, err
		}
	}

	// Clean any existing entry.
	if util.PathExists(devPath) {
		err := os.Remove(devPath)
		if err != nil {
			return nil, "", false, err
		}
	}

	// Create the mount point.
	if isFile {
		f, err := os.Create(devPath)
		if err != nil {
			return nil, "", false, err
		}

		_ = f.Close()
	} else {
		err := os.Mkdir(devPath, 0700)
		if err != nil {
			return nil, "", false, err
		}
	}

	if isReadOnly {
		mntOptions = append(mntOptions, "ro")
	}

	// Mount the fs.
	err := DiskMount(srcPath, devPath, isRecursive, d.config["propagation"], mntOptions, fsName)
	if err != nil {
		return nil, "", false, err
	}

	revert.Add(func() { _ = DiskMountClear(devPath) })

	cleanup := revert.Clone().Fail // Clone before calling revert.Success() so we can return the Fail func.
	revert.Success()
	return cleanup, devPath, isFile, err
}

// localSourceOpen opens a local disk source path and returns a file handle to it.
// If d.restrictedParentSourcePath has been set during validation, then the openat2 syscall is used to ensure that
// the srcPath opened doesn't resolve above the allowed parent source path.
func (d *disk) localSourceOpen(srcPath string) (*os.File, error) {
	var err error
	var f *os.File

	if d.restrictedParentSourcePath != "" {
		// Get relative srcPath in relation to allowed parent source path.
		relSrcPath, err := filepath.Rel(d.restrictedParentSourcePath, srcPath)
		if err != nil {
			return nil, fmt.Errorf("Failed resolving source path %q relative to restricted parent source path %q: %w", srcPath, d.restrictedParentSourcePath, err)
		}

		// Open file handle to parent for use with openat2 later.
		// Has to use unix.O_PATH to support directories and sockets.
		allowedParent, err := os.OpenFile(d.restrictedParentSourcePath, unix.O_PATH, 0)
		if err != nil {
			return nil, fmt.Errorf("Failed opening allowed parent source path %q: %w", d.restrictedParentSourcePath, err)
		}

		defer func() { _ = allowedParent.Close() }()

		// For restricted source paths we use openat2 to prevent resolving to a mount path above the
		// allowed parent source path. Requires Linux kernel >= 5.6.
		fd, err := unix.Openat2(int(allowedParent.Fd()), relSrcPath, &unix.OpenHow{
			Flags:   unix.O_PATH | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
		})
		if err != nil {
			if errors.Is(err, unix.EXDEV) {
				return nil, fmt.Errorf("Source path %q resolves outside of restricted parent source path %q", srcPath, d.restrictedParentSourcePath)
			}

			return nil, fmt.Errorf("Failed opening restricted source path %q: %w", srcPath, err)
		}

		f = os.NewFile(uintptr(fd), srcPath)
	} else {
		// Open file handle to local source. Has to use unix.O_PATH to support directories and sockets.
		f, err = os.OpenFile(srcPath, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("Failed opening source path %q: %w", srcPath, err)
		}
	}

	return f, nil
}

func (d *disk) storagePoolVolumeAttachShift(projectName, poolName, volumeName string, volumeType int, remapPath string) error {
	var err error
	var dbVolume *db.StorageVolume
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), projectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return err
	}

	poolVolumePut := dbVolume.StorageVolume.Writable()

	// Check if unmapped.
	if util.IsTrue(poolVolumePut.Config["security.unmapped"]) {
		// No need to look at containers and maps for unmapped volumes.
		return nil
	}

	// Get the on-disk idmap for the volume.
	var lastIdmap *idmap.Set
	if poolVolumePut.Config["volatile.idmap.last"] != "" {
		lastIdmap, err = idmap.NewSetFromJSON(poolVolumePut.Config["volatile.idmap.last"])
		if err != nil {
			d.logger.Error("Failed to unmarshal last idmapping", logger.Ctx{"idmap": poolVolumePut.Config["volatile.idmap.last"], "err": err})
			return err
		}
	}

	var nextIdmap *idmap.Set
	nextJSONMap := "[]"
	if util.IsFalseOrEmpty(poolVolumePut.Config["security.shifted"]) {
		c := d.inst.(instance.Container)
		// Get the container's idmap.
		if c.IsRunning() {
			nextIdmap, err = c.CurrentIdmap()
		} else {
			nextIdmap, err = c.NextIdmap()
		}

		if err != nil {
			return err
		}

		if nextIdmap != nil {
			nextJSONMap, err = nextIdmap.ToJSON()
			if err != nil {
				return err
			}
		}
	}

	poolVolumePut.Config["volatile.idmap.next"] = nextJSONMap

	if !nextIdmap.Equals(lastIdmap) {
		d.logger.Debug("Shifting storage volume")

		if util.IsFalseOrEmpty(poolVolumePut.Config["security.shifted"]) {
			volumeUsedBy := []instance.Instance{}
			err = storagePools.VolumeUsedByInstanceDevices(d.state, poolName, projectName, &dbVolume.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
				inst, err := instance.Load(d.state, dbInst, project)
				if err != nil {
					return err
				}

				volumeUsedBy = append(volumeUsedBy, inst)
				return nil
			})
			if err != nil {
				return err
			}

			if len(volumeUsedBy) > 1 {
				for _, inst := range volumeUsedBy {
					if inst.Type() != instancetype.Container {
						continue
					}

					ct := inst.(instance.Container)

					var ctNextIdmap *idmap.Set

					if ct.IsRunning() {
						ctNextIdmap, err = ct.CurrentIdmap()
					} else {
						ctNextIdmap, err = ct.NextIdmap()
					}

					if err != nil {
						return fmt.Errorf("Failed to retrieve idmap of container")
					}

					if !nextIdmap.Equals(ctNextIdmap) {
						return fmt.Errorf("Idmaps of container %q and storage volume %q are not identical", ct.Name(), volumeName)
					}
				}
			} else if len(volumeUsedBy) == 1 {
				// If we're the only one who's attached that container
				// we can shift the storage volume.
				// I'm not sure if we want some locking here.
				if volumeUsedBy[0].Name() != d.inst.Name() {
					return fmt.Errorf("Idmaps of container and storage volume are not identical")
				}
			}
		}

		// Unshift rootfs.
		if lastIdmap != nil {
			var err error

			if d.pool.Driver().Info().Name == "zfs" {
				err = lastIdmap.UnshiftPath(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = lastIdmap.UnshiftPath(remapPath, nil)
			}

			if err != nil {
				d.logger.Error("Failed to unshift", logger.Ctx{"path": remapPath, "err": err})
				return err
			}

			d.logger.Debug("Unshifted", logger.Ctx{"path": remapPath})
		}

		// Shift rootfs.
		if nextIdmap != nil {
			var err error

			if d.pool.Driver().Info().Name == "zfs" {
				err = nextIdmap.ShiftPath(remapPath, storageDrivers.ShiftZFSSkipper)
			} else {
				err = nextIdmap.ShiftPath(remapPath, nil)
			}

			if err != nil {
				d.logger.Error("Failed to shift", logger.Ctx{"path": remapPath, "err": err})
				return err
			}

			d.logger.Debug("Shifted", logger.Ctx{"path": remapPath})
		}

		d.logger.Debug("Shifted storage volume")
	}

	jsonIdmap, err := nextIdmap.ToJSON()
	if err != nil {
		d.logger.Error("Failed to marshal idmap", logger.Ctx{"idmap": nextIdmap, "err": err})
		return err
	}

	// Update last idmap.
	poolVolumePut.Config["volatile.idmap.last"] = jsonIdmap

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateStoragePoolVolume(ctx, projectName, volumeName, volumeType, d.pool.ID(), poolVolumePut.Description, poolVolumePut.Config)
	})
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *disk) Stop() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM {
		return d.stopVM()
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	// Figure out the paths
	relativeDestPath := strings.TrimPrefix(d.config["path"], "/")
	devPath := d.getDevicePath(d.name, d.config)

	// The disk device doesn't exist do nothing.
	if !util.PathExists(devPath) {
		return nil, nil
	}

	// Request an unmount of the device inside the instance.
	runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
		TargetPath: relativeDestPath,
	})

	return &runConf, nil
}

func (d *disk) stopVM() (*deviceConfig.RunConfig, error) {
	// Stop the virtfs-proxy-helper process and clean up.
	err := DiskVMVirtfsProxyStop(d.vmVirtfsProxyHelperPaths())
	if err != nil {
		return &deviceConfig.RunConfig{}, fmt.Errorf("Failed cleaning up virtfs-proxy-helper: %w", err)
	}

	// Stop the virtiofsd process and clean up.
	err = DiskVMVirtiofsdStop(d.vmVirtiofsdPaths())
	if err != nil {
		return &deviceConfig.RunConfig{}, fmt.Errorf("Failed cleaning up virtiofsd: %w", err)
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *disk) postStop() error {
	// Clean any existing device mount entry. Should occur first before custom volume unmounts.
	err := DiskMountClear(d.getDevicePath(d.name, d.config))
	if err != nil {
		return err
	}

	// Check if pool-specific action should be taken to unmount custom volume disks.
	if d.config["pool"] != "" && d.config["path"] != "/" {
		// Only custom volumes can be attached currently.
		storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		_, err = d.pool.UnmountCustomVolume(storageProjectName, d.config["source"], nil)
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			return err
		}
	}

	if d.sourceIsCeph() {
		v := d.volatileGet()
		err := diskCephRbdUnmap(v["ceph_rbd"])
		if err != nil {
			d.logger.Error("Failed to unmap RBD volume", logger.Ctx{"rbd": v["ceph_rbd"], "err": err})
		}
	}

	return nil
}

// getDiskLimits calculates Block I/O limits.
func (d *disk) getDiskLimits() (map[string]diskBlockLimit, error) {
	result := map[string]diskBlockLimit{}

	// Build a list of all valid block devices
	validBlocks := []string{}

	dents, err := os.ReadDir("/sys/class/block/")
	if err != nil {
		return nil, err
	}

	for _, f := range dents {
		fPath := filepath.Join("/sys/class/block/", f.Name())
		if util.PathExists(fmt.Sprintf("%s/partition", fPath)) {
			continue
		}

		if !util.PathExists(fmt.Sprintf("%s/dev", fPath)) {
			continue
		}

		block, err := os.ReadFile(fmt.Sprintf("%s/dev", fPath))
		if err != nil {
			return nil, err
		}

		validBlocks = append(validBlocks, strings.TrimSuffix(string(block), "\n"))
	}

	// Process all the limits
	blockLimits := map[string][]diskBlockLimit{}
	for devName, dev := range d.inst.ExpandedDevices() {
		if dev["type"] != "disk" {
			continue
		}

		// Apply max limit
		if dev["limits.max"] != "" {
			dev["limits.read"] = dev["limits.max"]
			dev["limits.write"] = dev["limits.max"]
		}

		// Parse the user input
		readBps, readIops, writeBps, writeIops, err := d.parseDiskLimit(dev["limits.read"], dev["limits.write"])
		if err != nil {
			return nil, err
		}

		// Set the source path
		source := d.getDevicePath(devName, dev)
		if dev["source"] == "" {
			source = d.inst.RootfsPath()
		}

		if !util.PathExists(source) {
			// Require that device is mounted before resolving block device if required.
			if d.isRequired(dev) {
				return nil, fmt.Errorf("Block device path doesn't exist %q", source)
			}

			continue // Do not resolve block device if device isn't mounted.
		}

		// Get the backing block devices (major:minor)
		blocks, err := d.getParentBlocks(source)
		if err != nil {
			if readBps == 0 && readIops == 0 && writeBps == 0 && writeIops == 0 {
				// If the device doesn't exist, there is no limit to clear so ignore the failure
				continue
			} else {
				return nil, err
			}
		}

		device := diskBlockLimit{readBps: readBps, readIops: readIops, writeBps: writeBps, writeIops: writeIops}
		for _, block := range blocks {
			blockStr := ""

			if slices.Contains(validBlocks, block) {
				// Straightforward entry (full block device)
				blockStr = block
			} else {
				// Attempt to deal with a partition (guess its parent)
				fields := strings.SplitN(block, ":", 2)
				fields[1] = "0"
				if slices.Contains(validBlocks, fmt.Sprintf("%s:%s", fields[0], fields[1])) {
					blockStr = fmt.Sprintf("%s:%s", fields[0], fields[1])
				}
			}

			if blockStr == "" {
				return nil, fmt.Errorf("Block device doesn't support quotas %q", block)
			}

			if blockLimits[blockStr] == nil {
				blockLimits[blockStr] = []diskBlockLimit{}
			}

			blockLimits[blockStr] = append(blockLimits[blockStr], device)
		}
	}

	// Average duplicate limits
	for block, limits := range blockLimits {
		var readBpsCount, readBpsTotal, readIopsCount, readIopsTotal, writeBpsCount, writeBpsTotal, writeIopsCount, writeIopsTotal int64

		for _, limit := range limits {
			if limit.readBps > 0 {
				readBpsCount++
				readBpsTotal += limit.readBps
			}

			if limit.readIops > 0 {
				readIopsCount++
				readIopsTotal += limit.readIops
			}

			if limit.writeBps > 0 {
				writeBpsCount++
				writeBpsTotal += limit.writeBps
			}

			if limit.writeIops > 0 {
				writeIopsCount++
				writeIopsTotal += limit.writeIops
			}
		}

		device := diskBlockLimit{}

		if readBpsCount > 0 {
			device.readBps = readBpsTotal / readBpsCount
		}

		if readIopsCount > 0 {
			device.readIops = readIopsTotal / readIopsCount
		}

		if writeBpsCount > 0 {
			device.writeBps = writeBpsTotal / writeBpsCount
		}

		if writeIopsCount > 0 {
			device.writeIops = writeIopsTotal / writeIopsCount
		}

		result[block] = device
	}

	return result, nil
}

func (d *disk) parseDiskLimit(readSpeed string, writeSpeed string) (int64, int64, int64, int64, error) {
	parseValue := func(value string) (int64, int64, error) {
		var err error

		bps := int64(0)
		iops := int64(0)

		if value == "" {
			return bps, iops, nil
		}

		if strings.HasSuffix(value, "iops") {
			iops, err = strconv.ParseInt(strings.TrimSuffix(value, "iops"), 10, 64)
			if err != nil {
				return -1, -1, err
			}
		} else {
			bps, err = units.ParseByteSizeString(value)
			if err != nil {
				return -1, -1, err
			}
		}

		return bps, iops, nil
	}

	readBps, readIops, err := parseValue(readSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	writeBps, writeIops, err := parseValue(writeSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	return readBps, readIops, writeBps, writeIops, nil
}

func (d *disk) getParentBlocks(path string) ([]string, error) {
	var devices []string
	var dev []string

	// Expand the mount path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	expPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		expPath = absPath
	}

	// Find the source mount of the path
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	match := ""
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)

		if len(rows[4]) <= len(match) {
			continue
		}

		if expPath != rows[4] && !strings.HasPrefix(expPath, rows[4]) {
			continue
		}

		match = rows[4]

		// Go backward to avoid problems with optional fields
		dev = []string{rows[2], rows[len(rows)-2]}
	}

	if dev == nil {
		return nil, fmt.Errorf("Couldn't find a match /proc/self/mountinfo entry")
	}

	// Handle the most simple case
	if !strings.HasPrefix(dev[0], "0:") {
		return []string{dev[0]}, nil
	}

	// Deal with per-filesystem oddities. We don't care about failures here
	// because any non-special filesystem => directory backend.
	fs, _ := linux.DetectFilesystem(expPath)

	if fs == "zfs" && util.PathExists("/dev/zfs") {
		// Accessible zfs filesystems
		poolName := strings.Split(dev[1], "/")[0]

		output, err := subprocess.RunCommand("zpool", "status", "-P", "-L", poolName)
		if err != nil {
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %q: %w", dev[1], err)
		}

		header := true
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			if fields[1] != "ONLINE" {
				continue
			}

			if header {
				header = false
				continue
			}

			var path string
			if util.PathExists(fields[0]) {
				if linux.IsBlockdevPath(fields[0]) {
					path = fields[0]
				} else {
					subDevices, err := d.getParentBlocks(fields[0])
					if err != nil {
						return nil, err
					}

					devices = append(devices, subDevices...)
				}
			} else {
				continue
			}

			if path != "" {
				_, major, minor, err := unixDeviceAttributes(path)
				if err != nil {
					continue
				}

				devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
			}
		}

		if len(devices) == 0 {
			return nil, fmt.Errorf("Unable to find backing block for zfs pool %q", poolName)
		}
	} else if fs == "btrfs" && util.PathExists(dev[1]) {
		// Accessible btrfs filesystems
		output, err := subprocess.RunCommand("btrfs", "filesystem", "show", dev[1])
		if err != nil {
			// Fallback to using device path to support BTRFS on block volumes (like LVM).
			_, major, minor, errFallback := unixDeviceAttributes(dev[1])
			if errFallback != nil {
				return nil, fmt.Errorf("Failed to query btrfs filesystem information for %q: %w", dev[1], err)
			}

			devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
		}

		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != "devid" {
				continue
			}

			_, major, minor, err := unixDeviceAttributes(fields[len(fields)-1])
			if err != nil {
				return nil, err
			}

			devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
		}
	} else if util.PathExists(dev[1]) {
		// Anything else with a valid path
		_, major, minor, err := unixDeviceAttributes(dev[1])
		if err != nil {
			return nil, err
		}

		devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
	} else {
		return nil, fmt.Errorf("Invalid block device %q", dev[1])
	}

	return devices, nil
}

// generateVMAgent generates an ISO containing the VM agent binary and config.
// Returns the path to the ISO.
func (d *disk) generateVMAgentDrive() (string, error) {
	scratchDir := filepath.Join(d.inst.DevicesPath(), linux.PathNameEncode(d.name))
	defer func() { _ = os.RemoveAll(scratchDir) }()

	// Check we have the mkisofs or genisoimage tool available.
	var mkisofsPath string
	var err error
	mkisofsPath, err = exec.LookPath("mkisofs")
	if err != nil {
		mkisofsPath, err = exec.LookPath("genisoimage")
		if err != nil {
			return "", fmt.Errorf("Neither mkisofs nor genisoimage could be found in $PATH")
		}
	}

	// Create agent drive dir.
	err = os.MkdirAll(scratchDir, 0100)
	if err != nil {
		return "", err
	}

	// Copy the instance config data over.
	configPath := filepath.Join(d.inst.Path(), "config")
	_, err = rsync.LocalCopy(configPath, scratchDir, "", false)
	if err != nil {
		return "", err
	}

	// Include the most likely agent.
	if util.PathExists(os.Getenv("INCUS_AGENT_PATH")) {
		agentInstallPath := filepath.Join(scratchDir, "incus-agent")

		os.Remove(agentInstallPath)

		err = internalUtil.FileCopy(filepath.Join(os.Getenv("INCUS_AGENT_PATH"), fmt.Sprintf("incus-agent.linux.%s", d.state.OS.Uname.Machine)), agentInstallPath)
		if err != nil {
			return "", err
		}

		err = os.Chmod(agentInstallPath, 0500)
		if err != nil {
			return "", err
		}

		err = os.Chown(agentInstallPath, 0, 0)
		if err != nil {
			return "", err
		}
	}

	// Finally convert the agent drive dir into an ISO file. The incus-agent label is important
	// as this is what incus-agent-loader uses to detect the drive.
	isoPath := filepath.Join(d.inst.Path(), "agent.iso")
	_, err = subprocess.RunCommand(mkisofsPath, "-joliet", "-rock", "-input-charset", "utf8", "-output-charset", "utf8", "-volid", "incus-agent", "-o", isoPath, scratchDir)
	if err != nil {
		return "", err
	}

	return isoPath, nil
}

// generateVMConfigDrive generates an ISO containing the cloud init config for a VM.
// Returns the path to the ISO.
func (d *disk) generateVMConfigDrive() (string, error) {
	scratchDir := filepath.Join(d.inst.DevicesPath(), linux.PathNameEncode(d.name))
	defer func() { _ = os.RemoveAll(scratchDir) }()

	// Check we have the mkisofs tool available.
	mkisofsPath, err := exec.LookPath("mkisofs")
	if err != nil {
		return "", err
	}

	// Create config drive dir.
	err = os.MkdirAll(scratchDir, 0100)
	if err != nil {
		return "", err
	}

	instanceConfig := d.inst.ExpandedConfig()

	// Use an empty vendor-data file if no custom vendor-data supplied.
	vendorData, ok := instanceConfig["cloud-init.vendor-data"]
	if !ok {
		vendorData = instanceConfig["user.vendor-data"]
		if vendorData == "" {
			vendorData = "#cloud-config\n{}"
		}
	}

	err = os.WriteFile(filepath.Join(scratchDir, "vendor-data"), []byte(vendorData), 0400)
	if err != nil {
		return "", err
	}

	// Use an empty user-data file if no custom user-data supplied.
	userData, ok := instanceConfig["cloud-init.user-data"]
	if !ok {
		userData = instanceConfig["user.user-data"]
		if userData == "" {
			userData = "#cloud-config\n{}"
		}
	}

	err = os.WriteFile(filepath.Join(scratchDir, "user-data"), []byte(userData), 0400)
	if err != nil {
		return "", err
	}

	// Include a network-config file if the user configured it.
	networkConfig, ok := instanceConfig["cloud-init.network-config"]
	if !ok {
		networkConfig = instanceConfig["user.network-config"]
	}

	if networkConfig != "" {
		err = os.WriteFile(filepath.Join(scratchDir, "network-config"), []byte(networkConfig), 0400)
		if err != nil {
			return "", err
		}
	}

	// Append any custom meta-data to our predefined meta-data config.
	metaData := fmt.Sprintf(`instance-id: %s
local-hostname: %s
%s
`, d.inst.Name(), d.inst.Name(), instanceConfig["user.meta-data"])

	err = os.WriteFile(filepath.Join(scratchDir, "meta-data"), []byte(metaData), 0400)
	if err != nil {
		return "", err
	}

	// Finally convert the config drive dir into an ISO file. The cidata label is important
	// as this is what cloud-init uses to detect, mount the drive and run the cloud-init
	// templates on first boot. The vendor-data template then modifies the system so that the
	// config drive is mounted and the agent is started on subsequent boots.
	isoPath := filepath.Join(d.inst.Path(), "config.iso")
	_, err = subprocess.RunCommand(mkisofsPath, "-joliet", "-rock", "-input-charset", "utf8", "-output-charset", "utf8", "-volid", "cidata", "-o", isoPath, scratchDir)
	if err != nil {
		return "", err
	}

	return isoPath, nil
}

// cephCreds returns cluster name and user name to use for ceph disks.
func (d *disk) cephCreds() (string, string) {
	// Apply the ceph configuration.
	userName := d.config["ceph.user_name"]
	if userName == "" {
		userName = storageDrivers.CephDefaultUser
	}

	clusterName := d.config["ceph.cluster_name"]
	if clusterName == "" {
		clusterName = storageDrivers.CephDefaultCluster
	}

	return clusterName, userName
}
