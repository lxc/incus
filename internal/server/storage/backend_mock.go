package storage

import (
	"io"
	"net/url"
	"time"

	"github.com/lxc/incus/internal/instancewriter"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/backup"
	backupConfig "github.com/lxc/incus/internal/server/backup/config"
	"github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/internal/server/storage/s3/miniod"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

type mockBackend struct {
	name   string
	state  *state.State
	logger logger.Logger
	driver drivers.Driver
}

func (b *mockBackend) ID() int64 {
	return 1 //  The tests expect the storage pool ID to be 1.
}

func (b *mockBackend) Name() string {
	return b.name
}

func (b *mockBackend) Description() string {
	return ""
}

func (b *mockBackend) ValidateName(value string) error {
	return nil
}

func (b *mockBackend) Validate(config map[string]string) error {
	return nil
}

func (b *mockBackend) Status() string {
	return api.NetworkStatusUnknown
}

func (b *mockBackend) LocalStatus() string {
	return api.NetworkStatusUnknown
}

func (b *mockBackend) ToAPI() api.StoragePool {
	return api.StoragePool{}
}

func (b *mockBackend) Driver() drivers.Driver {
	return b.driver
}

func (b *mockBackend) MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return []migration.Type{
		{
			FSType:   FallbackMigrationType(contentType),
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

func (b *mockBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}

func (b *mockBackend) IsUsed() (bool, error) {
	return false, nil
}

func (b *mockBackend) Delete(clientType request.ClientType, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Update(clientType request.ClientType, newDescription string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Create(clientType request.ClientType, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

func (b *mockBackend) ApplyPatch(name string) error {
	return nil
}

func (b *mockBackend) GetVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	return drivers.Volume{}
}

func (b *mockBackend) CreateInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error) {
	return nil, nil, nil
}

func (b *mockBackend) CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

func (b *mockBackend) GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

func (b *mockBackend) UpdateInstanceBackupFile(inst instance.Instance, snapshot bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, deleteMissing bool, op *operations.Operation) ([]*api.InstanceSnapshot, error) {
	return nil, nil
}

func (b *mockBackend) ListUnknownVolumes(op *operations.Operation) (map[string][]*backupConfig.Config, error) {
	return nil, nil
}

func (b *mockBackend) ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

func (b *mockBackend) MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CleanupInstancePaths(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RefreshCustomVolume(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error) {
	return nil, nil
}

func (b *mockBackend) SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstance(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

func (b *mockBackend) UnmountInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceSnapshot(i instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

func (b *mockBackend) UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) EnsureImage(fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateImage(fingerprint, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateBucket(projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteBucket(projectName string, bucketName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) ImportBucket(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

func (b *mockBackend) CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error) {
	return nil, nil
}

func (b *mockBackend) UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteBucketKey(projectName string, bucketName string, keyName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) ActivateBucket(projectName string, bucketName string, op *operations.Operation) (*miniod.Process, error) {
	return nil, nil
}

func (b *mockBackend) GetBucketURL(bucketName string) *url.URL {
	return nil
}

func (b *mockBackend) CreateCustomVolume(projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromCopy(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName string, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolume(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateCustomVolume(projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteCustomVolume(projectName string, volName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromMigration(projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetCustomVolumeDisk(projectName string, volName string) (string, error) {
	return "", nil
}

func (b *mockBackend) GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error) {
	return nil, nil
}

func (b *mockBackend) MountCustomVolume(projectName string, volName string, op *operations.Operation) (*MountInfo, error) {
	return nil, nil
}

func (b *mockBackend) UnmountCustomVolume(projectName string, volName string, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) ImportCustomVolume(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

func (b *mockBackend) CreateCustomVolumeSnapshot(projectName string, volName string, newSnapshotName string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolumeSnapshot(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteCustomVolumeSnapshot(projectName string, volName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateCustomVolumeSnapshot(projectName string, volName string, newDesc string, newConfig map[string]string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreCustomVolume(projectName string, volName string, snapshotName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromISO(projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error {
	return nil
}

// GenerateBucketBackupConfig returns the backup config entry for this bucket.
func (b *mockBackend) GenerateBucketBackupConfig(projectName string, bucketName string, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

// BackupBucket backups up a bucket to a tarball.
func (b *mockBackend) BackupBucket(projectName string, bucketName string, tarWriter *instancewriter.InstanceTarWriter, op *operations.Operation) error {
	return nil
}

// CreateBucketFromBackup creates a bucket from a tarball.
func (b *mockBackend) CreateBucketFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error {
	return nil
}
