package lifecycle

import (
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the volume interface.
type volume interface {
	Name() string
	Pool() string
}

// StorageVolumeAction represents a lifecycle event action for storage volumes.
type StorageVolumeAction string

// All supported lifecycle events for storage volumes.
const (
	StorageVolumeCreated  = StorageVolumeAction(api.EventLifecycleStorageVolumeCreated)
	StorageVolumeDeleted  = StorageVolumeAction(api.EventLifecycleStorageVolumeDeleted)
	StorageVolumeUpdated  = StorageVolumeAction(api.EventLifecycleStorageVolumeUpdated)
	StorageVolumeRenamed  = StorageVolumeAction(api.EventLifecycleStorageVolumeRenamed)
	StorageVolumeRestored = StorageVolumeAction(api.EventLifecycleStorageVolumeRestored)
)

// Event creates the lifecycle event for an action on a storage volume.
func (a StorageVolumeAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "storage-pools", v.Pool(), "volumes", volumeType, v.Name()).Project(projectName)

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
