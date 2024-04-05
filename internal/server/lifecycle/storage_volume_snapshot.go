package lifecycle

import (
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// StorageVolumeSnapshotAction represents a lifecycle event action for storage volume snapshots.
type StorageVolumeSnapshotAction string

// All supported lifecycle events for storage volume snapshots.
const (
	StorageVolumeSnapshotCreated = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotCreated)
	StorageVolumeSnapshotDeleted = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotDeleted)
	StorageVolumeSnapshotUpdated = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotUpdated)
	StorageVolumeSnapshotRenamed = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotRenamed)
)

// Event creates the lifecycle event for an action on a storage volume snapshot.
func (a StorageVolumeSnapshotAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]any) api.EventLifecycle {
	parentName, snapshotName, _ := api.GetParentAndSnapshotName(v.Name())

	u := api.NewURL().Path(version.APIVersion, "storage-pools", v.Pool(), "volumes", volumeType, parentName, "snapshots", snapshotName).Project(projectName)

	var requestor *api.EventLifecycleRequestor
	if op != nil {
		requestor = op.Requestor()
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
