package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ClusterAction represents a lifecycle event action for clusters.
type ClusterAction string

// All supported lifecycle events for clusters.
const (
	ClusterEnabled            = ClusterAction(api.EventLifecycleClusterEnabled)
	ClusterDisabled           = ClusterAction(api.EventLifecycleClusterDisabled)
	ClusterCertificateUpdated = ClusterAction(api.EventLifecycleClusterCertificateUpdated)
	ClusterTokenCreated       = ClusterAction(api.EventLifecycleClusterTokenCreated)
)

// Event creates the lifecycle event for an action on a cluster.
func (a ClusterAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "cluster", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
