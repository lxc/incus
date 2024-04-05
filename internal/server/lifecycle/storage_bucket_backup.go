package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// StorageBucketBackupAction represents a lifecycle event action for storage bucket backups.
type StorageBucketBackupAction string

// All supported lifecycle events for storage volume backups.
const (
	StorageBucketBackupCreated   = StorageBucketBackupAction(api.EventLifecycleStorageBucketBackupCreated)
	StorageBucketBackupDeleted   = StorageBucketBackupAction(api.EventLifecycleStorageBucketBackupDeleted)
	StorageBucketBackupRetrieved = StorageBucketBackupAction(api.EventLifecycleStorageBucketBackupRetrieved)
	StorageBucketBackupRenamed   = StorageBucketBackupAction(api.EventLifecycleStorageBucketBackupRenamed)
)

// Event creates the lifecycle event for an action on a storage volume backup.
func (a StorageBucketBackupAction) Event(poolName string, fullBackupName string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	bucketName, backupName, _ := api.GetParentAndSnapshotName(fullBackupName)

	u := api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "buckets", bucketName, "backups", backupName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
