package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the network interface.
type network interface {
	Name() string
	Project() string
}

// NetworkAction represents a lifecycle event action for network devices.
type NetworkAction string

// All supported lifecycle events for network devices.
const (
	NetworkCreated = NetworkAction(api.EventLifecycleNetworkCreated)
	NetworkDeleted = NetworkAction(api.EventLifecycleNetworkDeleted)
	NetworkUpdated = NetworkAction(api.EventLifecycleNetworkUpdated)
	NetworkRenamed = NetworkAction(api.EventLifecycleNetworkRenamed)
)

// Event creates the lifecycle event for an action on a network device.
func (a NetworkAction) Event(n network, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "networks", n.Name()).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
