package drivers

import (
	"io"
	"net/url"

	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/backup"
	"github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators)
	load() error
	isRemote() bool
}

// Driver represents a low-level storage driver.
type Driver interface {
	// Internal.
	Info() Info
	HasVolume(vol Volume) (bool, error)
	roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) int64
	isBlockBacked(vol Volume) bool

	// Export struct details.
	Name() string
	Config() map[string]string
	Logger() logger.Logger

	// Pool.
	FillConfig() error
	Create() error
	Delete(op *operations.Operation) error
	// Mount mounts a storage pool if needed, returns true if we caused a new mount, false if already mounted.
	Mount() (bool, error)

	// Unmount unmounts a storage pool if needed, returns true if unmounted, false if was not mounted.
	Unmount() (bool, error)
	GetResources() (*api.ResourcesStoragePool, error)
	Validate(config map[string]string) error
	Update(changedConfig map[string]string) error
	ApplyPatch(name string) error

	// Buckets.
	ValidateBucket(bucket Volume) error
	GetBucketURL(bucketName string) *url.URL
	CreateBucket(bucket Volume, op *operations.Operation) error
	DeleteBucket(bucket Volume, op *operations.Operation) error
	UpdateBucket(bucket Volume, changedConfig map[string]string) error
	ValidateBucketKey(keyName string, creds S3Credentials, roleName string) error
	CreateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error)
	UpdateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error)
	DeleteBucketKey(bucket Volume, keyName string, op *operations.Operation) error

	// Volumes.
	FillVolumeConfig(vol Volume) error
	ValidateVolume(vol Volume, removeUnknownKeys bool) error
	CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error
	CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, allowInconsistent bool, op *operations.Operation) error
	RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, allowInconsistent bool, op *operations.Operation) error
	DeleteVolume(vol Volume, op *operations.Operation) error
	RenameVolume(vol Volume, newName string, op *operations.Operation) error
	UpdateVolume(vol Volume, changedConfig map[string]string) error
	GetVolumeUsage(vol Volume) (int64, error)
	SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error
	GetVolumeDiskPath(vol Volume) (string, error)
	ListVolumes() ([]Volume, error)

	// MountVolume mounts a storage volume (if not mounted) and increments reference counter.
	MountVolume(vol Volume, op *operations.Operation) error

	// MountVolumeSnapshot mounts a storage volume snapshot as readonly.
	MountVolumeSnapshot(snapVol Volume, op *operations.Operation) error

	// CanDelegateVolume checks whether the volume can be delegated.
	CanDelegateVolume(vol Volume) bool

	// DelegateVolume allows for the volume to be managed by the instance.
	DelegateVolume(vol Volume, pid int) error

	// UnmountVolume unmounts a storage volume, returns true if unmounted, false if was not
	// mounted.
	UnmountVolume(vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume snapshot, returns true if unmounted, false if was
	// not mounted.
	UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error)

	CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error
	DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error
	RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error
	VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error)
	RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error

	// Migration.
	MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type
	MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error
	CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error

	// Backup.
	BackupVolume(vol Volume, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error
	CreateVolumeFromBackup(vol Volume, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error)
}
