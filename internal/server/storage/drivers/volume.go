package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/revert"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/locking"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/refcount"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
)

// tmpVolSuffix Suffix to use for any temporary volumes created by Incus.
const tmpVolSuffix = ".incustmp"

// isoVolSuffix suffix used for iso content type volumes.
const isoVolSuffix = ".iso"

// DefaultBlockSize is the default size of block volumes.
const DefaultBlockSize = "10GiB"

// DefaultFilesystem filesytem to use for block devices by default.
const DefaultFilesystem = "ext4"

// defaultFilesystemMountOpts mount options to use for filesystem block devices by default.
const defaultFilesystemMountOptions = "discard"

// volIDQuotaSkip is used to indicate to drivers that quotas should not be setup, used during backup import.
const volIDQuotaSkip = int64(-1)

// VolumeType represents a storage volume type.
type VolumeType string

// IsInstance indicates if the VolumeType represents an instance type.
func (t VolumeType) IsInstance() bool {
	if t == VolumeTypeContainer || t == VolumeTypeVM {
		return true
	}

	return false
}

// VolumeTypeBucket represents a bucket storage volume.
const VolumeTypeBucket = VolumeType("buckets")

// VolumeTypeImage represents an image storage volume.
const VolumeTypeImage = VolumeType("images")

// VolumeTypeCustom represents a custom storage volume.
const VolumeTypeCustom = VolumeType("custom")

// VolumeTypeContainer represents a container storage volume.
const VolumeTypeContainer = VolumeType("containers")

// VolumeTypeVM represents a virtual-machine storage volume.
const VolumeTypeVM = VolumeType("virtual-machines")

// ContentType indicates the format of the volume.
type ContentType string

// ContentTypeFS indicates the volume will be populated with a mountabble filesystem.
const ContentTypeFS = ContentType("filesystem")

// ContentTypeBlock indicates the volume will be a block device and its contents and we do not
// know which filesystem(s) (if any) are in use.
const ContentTypeBlock = ContentType("block")

// ContentTypeISO indicates the volume will be an ISO which is read-only, and uses the ISO 9660 filesystem.
const ContentTypeISO = ContentType("iso")

// VolumePostHook function returned from a storage action that should be run later to complete the action.
type VolumePostHook func(vol Volume) error

// BaseDirectories maps volume types to the expected directories.
var BaseDirectories = map[VolumeType][]string{
	VolumeTypeBucket:    {"buckets"},
	VolumeTypeContainer: {"containers", "containers-snapshots"},
	VolumeTypeCustom:    {"custom", "custom-snapshots"},
	VolumeTypeImage:     {"images"},
	VolumeTypeVM:        {"virtual-machines", "virtual-machines-snapshots"},
}

// Volume represents a storage volume, and provides functions to mount and unmount it.
type Volume struct {
	name                 string
	pool                 string
	poolConfig           map[string]string
	volType              VolumeType
	contentType          ContentType
	config               map[string]string
	driver               Driver
	mountCustomPath      string // Mount the filesystem volume at a custom location.
	mountFilesystemProbe bool   // Probe filesystem type when mounting volume (when needed).
	hasSource            bool   // Whether the volume is created from a source volume.
}

// NewVolume instantiates a new Volume struct.
func NewVolume(driver Driver, poolName string, volType VolumeType, contentType ContentType, volName string, volConfig map[string]string, poolConfig map[string]string) Volume {
	return Volume{
		name:        volName,
		pool:        poolName,
		poolConfig:  poolConfig,
		volType:     volType,
		contentType: contentType,
		config:      volConfig,
		driver:      driver,
	}
}

// Name returns volume's name.
func (v Volume) Name() string {
	return v.name
}

// Pool returns the volume's pool name.
func (v Volume) Pool() string {
	return v.pool
}

// Config returns the volume's (unexpanded) config.
func (v Volume) Config() map[string]string {
	return v.config
}

// ExpandedConfig returns either the value of the volume's config key or the pool's config "volume.{key}" value.
func (v Volume) ExpandedConfig(key string) string {
	volVal, ok := v.config[key]
	if ok {
		return volVal
	}

	return v.poolConfig[fmt.Sprintf("volume.%s", key)]
}

// NewSnapshot instantiates a new Volume struct representing a snapshot of the parent volume.
func (v Volume) NewSnapshot(snapshotName string) (Volume, error) {
	if v.IsSnapshot() {
		return Volume{}, fmt.Errorf("Cannot create a snapshot volume from a snapshot")
	}

	fullSnapName := GetSnapshotVolumeName(v.name, snapshotName)
	vol := NewVolume(v.driver, v.pool, v.volType, v.contentType, fullSnapName, v.config, v.poolConfig)

	// Propagate filesystem probe mode of parent volume.
	vol.SetMountFilesystemProbe(v.mountFilesystemProbe)

	return vol, nil
}

// IsSnapshot indicates if volume is a snapshot.
func (v Volume) IsSnapshot() bool {
	return internalInstance.IsSnapshot(v.name)
}

// MountPath returns the path where the volume will be mounted.
func (v Volume) MountPath() string {
	if v.mountCustomPath != "" {
		return v.mountCustomPath
	}

	volName := v.name

	if v.volType == VolumeTypeCustom && v.contentType == ContentTypeISO {
		volName = fmt.Sprintf("%s%s", volName, isoVolSuffix)
	}

	return GetVolumeMountPath(v.pool, v.volType, volName)
}

// mountLockName returns the lock name to use for mount/unmount operations on a volume.
func (v Volume) mountLockName() string {
	return OperationLockName("Mount", v.pool, v.volType, v.contentType, v.name)
}

// MountLock attempts to lock the mount lock for the volume and returns the UnlockFunc.
func (v Volume) MountLock() (locking.UnlockFunc, error) {
	return locking.Lock(context.TODO(), v.mountLockName())
}

// MountRefCountIncrement increments the mount ref counter for the volume and returns the new value.
func (v Volume) MountRefCountIncrement() uint {
	return refcount.Increment(v.mountLockName(), 1)
}

// MountRefCountDecrement decrements the mount ref counter for the volume and returns the new value.
func (v Volume) MountRefCountDecrement() uint {
	return refcount.Decrement(v.mountLockName(), 1)
}

// MountInUse returns whether the volume has a mount ref counter >0.
func (v Volume) MountInUse() bool {
	return refcount.Get(v.mountLockName()) > 0
}

// EnsureMountPath creates the volume's mount path if missing, then sets the correct permission for the type.
// If permission setting fails and the volume is a snapshot then the error is ignored as snapshots are read only.
func (v Volume) EnsureMountPath() error {
	volPath := v.MountPath()

	revert := revert.New()
	defer revert.Fail()

	// Create volume's mount path if missing, with any created directories set to 0711.
	if !util.PathExists(volPath) {
		if v.IsSnapshot() {
			// Create the parent directory if needed.
			parentName, _, _ := api.GetParentAndSnapshotName(v.name)
			err := createParentSnapshotDirIfMissing(v.pool, v.volType, parentName)
			if err != nil {
				return err
			}
		}

		err := os.Mkdir(volPath, 0711)
		if err != nil {
			return fmt.Errorf("Failed to create mount directory %q: %w", volPath, err)
		}

		revert.Add(func() { _ = os.Remove(volPath) })
	}

	// Set very restrictive mode 0100 for non-custom, non-bucket and non-image volumes.
	mode := os.FileMode(0711)
	if v.volType != VolumeTypeCustom && v.volType != VolumeTypeImage && v.volType != VolumeTypeBucket {
		mode = os.FileMode(0100)
	}

	fInfo, err := os.Lstat(volPath)
	if err != nil {
		return fmt.Errorf("Error getting mount directory info %q: %w", volPath, err)
	}

	// We expect the mount path to be a directory, so use this for comparison.
	compareMode := os.ModeDir | mode

	// Set mode of actual volume's mount path if needed.
	if fInfo.Mode() != compareMode {
		err = os.Chmod(volPath, mode)

		// If the chmod failed, return the error as long as the volume is not a snapshot.
		// If the volume is a snapshot, we must ignore the error as snapshots are readonly and cannot be
		// modified after they are taken, such that any permission error is not fixable at mount time.
		if err != nil && !v.IsSnapshot() {
			return fmt.Errorf("Failed to chmod mount directory %q (%04o): %w", volPath, mode, err)
		}
	}

	revert.Success()
	return nil
}

// MountTask runs the supplied task after mounting the volume if needed. If the volume was mounted
// for this then it is unmounted when the task finishes.
func (v Volume) MountTask(task func(mountPath string, op *operations.Operation) error, op *operations.Operation) error {
	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	var err error

	if v.IsSnapshot() {
		err = v.driver.MountVolumeSnapshot(v, op)
	} else {
		err = v.driver.MountVolume(v, op)
	}

	if err != nil {
		return err
	}

	taskErr := task(v.MountPath(), op)

	// Try and unmount, even on task error.
	if v.IsSnapshot() {
		_, err = v.driver.UnmountVolumeSnapshot(v, op)
	} else {
		_, err = v.driver.UnmountVolume(v, false, op)
	}

	// Return task error if failed.
	if taskErr != nil {
		return taskErr
	}

	// Return unmount error if failed.
	if err != nil && !errors.Is(err, ErrInUse) {
		return err
	}

	return nil
}

// UnmountTask runs the supplied task after unmounting the volume if needed.
// If the volume was unmounted for this then it is mounted when the task finishes.
// keepBlockDev indicates if backing block device should be not be deactivated if volume is unmounted.
func (v Volume) UnmountTask(task func(op *operations.Operation) error, keepBlockDev bool, op *operations.Operation) error {
	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if v.IsSnapshot() {
		ourUnmount, err := v.driver.UnmountVolumeSnapshot(v, op)
		if err != nil {
			return err
		}

		if ourUnmount {
			defer func() { _ = v.driver.MountVolumeSnapshot(v, op) }()
		}
	} else {
		ourUnmount, err := v.driver.UnmountVolume(v, keepBlockDev, op)
		if err != nil {
			return err
		}

		if ourUnmount {
			defer func() { _ = v.driver.MountVolume(v, op) }()
		}
	}

	return task(op)
}

// Snapshots returns a list of snapshots for the volume (in no particular order).
func (v Volume) Snapshots(op *operations.Operation) ([]Volume, error) {
	if v.IsSnapshot() {
		return nil, fmt.Errorf("Volume is a snapshot")
	}

	snapshots, err := v.driver.VolumeSnapshots(v, op)
	if err != nil {
		return nil, err
	}

	snapVols := make([]Volume, 0, len(snapshots))
	for _, snapName := range snapshots {
		snapshot, err := v.NewSnapshot(snapName)
		if err != nil {
			return nil, err
		}

		snapVols = append(snapVols, snapshot)
	}

	return snapVols, nil
}

// SnapshotsMatch checks that the snapshots, according to the storage driver, match those provided (although not
// necessarily in the same order).
func (v Volume) SnapshotsMatch(snapNames []string, op *operations.Operation) error {
	if v.IsSnapshot() {
		return fmt.Errorf("Volume is a snapshot")
	}

	snapshots, err := v.driver.VolumeSnapshots(v, op)
	if err != nil {
		return err
	}

	for _, snapName := range snapNames {
		if !util.ValueInSlice(snapName, snapshots) {
			return fmt.Errorf("Snapshot %q expected but not in storage", snapName)
		}
	}

	for _, snapshot := range snapshots {
		if !util.ValueInSlice(snapshot, snapNames) {
			return fmt.Errorf("Snapshot %q in storage but not expected", snapshot)
		}
	}

	return nil
}

// IsBlockBacked indicates whether storage device is block backed.
func (v Volume) IsBlockBacked() bool {
	return v.driver.isBlockBacked(v) || v.mountFilesystemProbe
}

// Type returns the volume type.
func (v Volume) Type() VolumeType {
	return v.volType
}

// ContentType returns the content type.
func (v Volume) ContentType() ContentType {
	return v.contentType
}

// IsVMBlock returns true if volume is a block volume for virtual machines or associated images.
func (v Volume) IsVMBlock() bool {
	return (v.volType == VolumeTypeVM || v.volType == VolumeTypeImage) && v.contentType == ContentTypeBlock
}

// IsCustomBlock returns true if volume is a custom block volume.
func (v Volume) IsCustomBlock() bool {
	return (v.volType == VolumeTypeCustom && v.contentType == ContentTypeBlock)
}

// NewVMBlockFilesystemVolume returns a copy of the volume with the content type set to ContentTypeFS and the
// config "size" property set to "size.state" or DefaultVMBlockFilesystemSize if not set.
func (v Volume) NewVMBlockFilesystemVolume() Volume {
	// Copy volume config so modifications don't affect original volume.
	newConf := make(map[string]string, len(v.config))
	for k, v := range v.config {
		if k == "zfs.block_mode" {
			continue // VM filesystem volumes never use ZFS block mode.
		}

		newConf[k] = v
	}

	if v.config["size.state"] != "" {
		newConf["size"] = v.config["size.state"]
	} else {
		// Fallback to the default VM filesystem size.
		newConf["size"] = deviceConfig.DefaultVMBlockFilesystemSize
	}

	vol := NewVolume(v.driver, v.pool, v.volType, ContentTypeFS, v.name, newConf, v.poolConfig)

	// Propagate filesystem probe mode of parent volume.
	vol.SetMountFilesystemProbe(v.mountFilesystemProbe)

	return vol
}

// SetQuota calls SetVolumeQuota on the Volume's driver.
func (v Volume) SetQuota(size string, allowUnsafeResize bool, op *operations.Operation) error {
	return v.driver.SetVolumeQuota(v, size, allowUnsafeResize, op)
}

// SetConfigSize sets the size config property on the Volume (does not resize volume).
func (v Volume) SetConfigSize(size string) {
	v.config["size"] = size
}

// SetConfigStateSize sets the size.state config property on the Volume (does not resize volume).
func (v Volume) SetConfigStateSize(size string) {
	v.config["size.state"] = size
}

// ConfigBlockFilesystem returns the filesystem to use for block volumes. Returns config value "block.filesystem"
// if defined in volume or pool's volume config, otherwise the DefaultFilesystem.
func (v Volume) ConfigBlockFilesystem() string {
	fs := v.ExpandedConfig("block.filesystem")
	if fs != "" {
		return fs
	}

	return DefaultFilesystem
}

// ConfigBlockMountOptions returns the filesystem mount options to use for block volumes. Returns config value
// "block.mount_options" if defined in volume or pool's volume config, otherwise defaultFilesystemMountOptions.
func (v Volume) ConfigBlockMountOptions() string {
	fs := v.ExpandedConfig("block.mount_options")
	if fs != "" {
		return fs
	}

	// Use some special options if the filesystem for the volume is BTRFS.
	if v.ConfigBlockFilesystem() == "btrfs" {
		return "user_subvol_rm_allowed,discard"
	}

	return defaultFilesystemMountOptions
}

// ConfigSize returns the size to use when creating new a volume. Returns config value "size" if defined in volume
// or pool's volume config, otherwise for block volumes and block-backed volumes the defaultBlockSize. For other
// volumes an empty string is returned if no size is defined.
func (v Volume) ConfigSize() string {
	size := v.ExpandedConfig("size")

	// If volume size isn't defined in either volume or pool config, then for block volumes or block-backed
	// volumes return the defaultBlockSize.
	if (size == "" || size == "0") && (v.contentType == ContentTypeBlock || v.IsBlockBacked()) {
		return DefaultBlockSize
	}

	// Return defined size or empty string if not defined.
	return size
}

// ConfigSizeFromSource derives the volume size to use for a new volume when copying from a source volume.
// Where possible (if the source volume has a volatile.rootfs.size property), it checks that the source volume
// isn't larger than the volume's "size" setting and the pool's "volume.size" setting.
func (v Volume) ConfigSizeFromSource(srcVol Volume) (string, error) {
	// If source is not an image, then only use volume specified size. This is so the pool volume size isn't
	// taken into account for non-image volume copies.
	if srcVol.volType != VolumeTypeImage {
		return v.config["size"], nil
	}

	// VM config filesystem volumes should always have a fixed specified size, so just return volume size.
	if v.volType == VolumeTypeVM && v.contentType == ContentTypeFS {
		return v.config["size"], nil
	}

	// If the source image doesn't have any size information, then use volume/pool/default size in that order.
	if srcVol.config["volatile.rootfs.size"] == "" {
		return v.ConfigSize(), nil
	}

	imgSizeBytes, err := units.ParseByteSizeString(srcVol.config["volatile.rootfs.size"])
	if err != nil {
		return "", err
	}

	// If volume/pool size is specified (excluding default size), then check it against the image minimum size.
	volSize := v.ExpandedConfig("size")
	if volSize != "" && volSize != "0" {
		volSizeBytes, err := units.ParseByteSizeString(volSize)
		if err != nil {
			return volSize, err
		}

		// Round the vol size (for comparison only) because some storage drivers round volumes they create,
		// and so the published images created from those volumes will also be rounded and will not be
		// directly usable with the same size setting without also rounding for this check.
		// Because we are not altering the actual size returned to use for the new volume, this will not
		// affect storage drivers that do not use rounding.
		volSizeBytes = v.driver.roundVolumeBlockSizeBytes(volSizeBytes)

		// The volume/pool specified size is smaller than image minimum size. We must not continue as
		// these specified sizes provide protection against unpacking a massive image and filling the pool.
		if volSizeBytes < imgSizeBytes {
			return "", fmt.Errorf("Source image size (%d) exceeds specified volume size (%d)", imgSizeBytes, volSizeBytes)
		}

		// Use the specified volume size.
		return volSize, nil
	}

	// If volume/pool size not specified above, then fallback to default volume size (if relevant) and compare.
	volSize = v.ConfigSize()
	if volSize != "" && volSize != "0" {
		volSizeBytes, err := units.ParseByteSizeString(volSize)
		if err != nil {
			return "", err
		}

		// Use image minimum size as volSize if the default volume size is smaller.
		if volSizeBytes < imgSizeBytes {
			return srcVol.config["volatile.rootfs.size"], nil
		}
	}

	// Use the default volume size.
	return volSize, nil
}

// SetMountFilesystemProbe enables or disables the probing mode when mounting the filesystem volume.
func (v *Volume) SetMountFilesystemProbe(probe bool) {
	v.mountFilesystemProbe = probe
}

// SetHasSource indicates whether the Volume is created from a source.
func (v *Volume) SetHasSource(hasSource bool) {
	v.hasSource = hasSource
}

// Clone returns a copy of the volume.
func (v Volume) Clone() Volume {
	// Copy the config map to avoid internal modifications affecting external state.
	newConfig := make(map[string]string, len(v.config))
	for k, v := range v.config {
		newConfig[k] = v
	}

	// Copy the pool config map to avoid internal modifications affecting external state.
	newPoolConfig := make(map[string]string, len(v.poolConfig))
	for k, v := range v.poolConfig {
		newPoolConfig[k] = v
	}

	return NewVolume(v.driver, v.pool, v.volType, v.contentType, v.name, newConfig, newPoolConfig)
}
