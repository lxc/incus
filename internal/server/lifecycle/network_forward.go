package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkForwardAction represents a lifecycle event action for network forwards.
type NetworkForwardAction string

// All supported lifecycle events for network forwards.
const (
	NetworkForwardCreated = NetworkForwardAction(api.EventLifecycleNetworkForwardCreated)
	NetworkForwardDeleted = NetworkForwardAction(api.EventLifecycleNetworkForwardDeleted)
	NetworkForwardUpdated = NetworkForwardAction(api.EventLifecycleNetworkForwardUpdated)
)

// Event creates the lifecycle event for an action on a network forward.
func (a NetworkForwardAction) Event(n network, listenAddress string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "forwards", listenAddress).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
