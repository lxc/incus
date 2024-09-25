package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ClusterGroupAction represents a lifecycle event action for cluster groups.
type ClusterGroupAction string

// All supported lifecycle events for cluster groups.
const (
	ClusterGroupCreated = ClusterGroupAction(api.EventLifecycleClusterGroupCreated)
	ClusterGroupDeleted = ClusterGroupAction(api.EventLifecycleClusterGroupDeleted)
	ClusterGroupUpdated = ClusterGroupAction(api.EventLifecycleClusterGroupUpdated)
	ClusterGroupRenamed = ClusterGroupAction(api.EventLifecycleClusterGroupRenamed)
)

// Event creates the lifecycle event for an action on a cluster group.
func (a ClusterGroupAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "cluster", "groups", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
