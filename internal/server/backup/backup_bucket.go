package backup

import (
	"context"
	"os"
	"time"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/state"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// BucketBackup represents a bucket backup.
type BucketBackup struct {
	CommonBackup

	projectName string
	poolName    string
	bucketName  string
}

// NewBucketBackup instantiates a new BucketBackup struct.
func NewBucketBackup(s *state.State, projectName, poolName, bucketName string, ID int, name string, creationDate, expiryDate time.Time) *BucketBackup {
	return &BucketBackup{
		CommonBackup: CommonBackup{
			state:        s,
			id:           ID,
			name:         name,
			creationDate: creationDate,
			expiryDate:   expiryDate,
		},
		projectName: projectName,
		poolName:    poolName,
		bucketName:  bucketName,
	}
}

// Delete removes a bucket backup.
func (b *BucketBackup) Delete() error {
	backupPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, b.name))
	// Delete the on-disk data.
	if util.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the bucket directory.
	backupsPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, b.bucketName))
	empty, _ := internalUtil.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteStoragePoolBucketBackup(ctx, b.name)
	})
	if err != nil {
		return err
	}

	return nil
}

// Rename renames a bucket backup.
func (b *BucketBackup) Rename(newName string) error {
	oldBackupPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, b.name))
	newBackupPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, newName))

	// Extract the old and new parent backup paths from the old and new backup names rather than use
	// bucket.Name() as this may be in flux if the bucket itself is being renamed, whereas the relevant
	// bucket name is encoded into the backup names.
	oldParentName, _, _ := api.GetParentAndSnapshotName(b.name)
	oldParentBackupsPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, oldParentName))
	newParentName, _, _ := api.GetParentAndSnapshotName(newName)
	newParentBackupsPath := internalUtil.VarPath("backups", "buckets", b.poolName, project.StorageBucket(b.projectName, newParentName))

	reverter := revert.New()
	defer reverter.Fail()

	// Create the new backup path if doesn't exist.
	if !util.PathExists(newParentBackupsPath) {
		err := os.MkdirAll(newParentBackupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Rename the backup directory.
	err := os.Rename(oldBackupPath, newBackupPath)
	if err != nil {
		return err
	}

	reverter.Add(func() { _ = os.Rename(newBackupPath, oldBackupPath) })

	// Check if we can remove the old parent directory.
	empty, _ := internalUtil.PathIsEmpty(oldParentBackupsPath)
	if empty {
		err := os.Remove(oldParentBackupsPath)
		if err != nil {
			return err
		}
	}

	// Rename the database record.
	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameBucketBackup(ctx, b.name, newName)
	})
	if err != nil {
		return err
	}

	reverter.Success()
	return nil
}

// Render returns a BucketBackup struct of the backup.
func (b *BucketBackup) Render() *api.StorageBucketBackup {
	return &api.StorageBucketBackup{
		Name:                 b.name,
		ExpiresAt:            b.expiryDate,
		CompressionAlgorithm: b.compressionAlgorithm,
	}
}
