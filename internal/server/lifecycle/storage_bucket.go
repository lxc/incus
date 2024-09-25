package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the pool interface.
type pool interface {
	Name() string
}

// StorageBucketAction represents a lifecycle event action for storage buckets.
type StorageBucketAction string

// StorageBucketKeyAction represents a lifecycle event action for storage bucket keys.
type StorageBucketKeyAction string

// All supported lifecycle events for storage buckets and keys.
const (
	StorageBucketCreated    = StorageBucketAction(api.EventLifecycleStorageBucketCreated)
	StorageBucketDeleted    = StorageBucketAction(api.EventLifecycleStorageBucketDeleted)
	StorageBucketUpdated    = StorageBucketAction(api.EventLifecycleStorageBucketUpdated)
	StorageBucketKeyCreated = StorageBucketKeyAction(api.EventLifecycleStorageBucketKeyCreated)
	StorageBucketKeyDeleted = StorageBucketKeyAction(api.EventLifecycleStorageBucketKeyDeleted)
	StorageBucketKeyUpdated = StorageBucketKeyAction(api.EventLifecycleStorageBucketKeyUpdated)
)

// Event creates the lifecycle event for an action on a storage bucket.
func (a StorageBucketAction) Event(pool pool, projectName string, bucketName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on a storage bucket.
func (a StorageBucketKeyAction) Event(pool pool, projectName string, bucketName string, keyName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName, "keys", keyName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
