package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ClusterMemberAction represents a lifecycle event action for cluster members.
type ClusterMemberAction string

// All supported lifecycle events for cluster members.
const (
	ClusterMemberAdded     = ClusterMemberAction(api.EventLifecycleClusterMemberAdded)
	ClusterMemberEvacuated = ClusterMemberAction(api.EventLifecycleClusterMemberEvacuated)
	ClusterMemberHealed    = ClusterMemberAction(api.EventLifecycleClusterMemberHealed)
	ClusterMemberRemoved   = ClusterMemberAction(api.EventLifecycleClusterMemberRemoved)
	ClusterMemberRenamed   = ClusterMemberAction(api.EventLifecycleClusterMemberRenamed)
	ClusterMemberRestored  = ClusterMemberAction(api.EventLifecycleClusterMemberRestored)
	ClusterMemberUpdated   = ClusterMemberAction(api.EventLifecycleClusterMemberUpdated)
)

// Event creates the lifecycle event for an action on a cluster member.
func (a ClusterMemberAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "cluster", "members", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
