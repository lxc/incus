package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkPeerAction represents a lifecycle event action for network peers.
type NetworkPeerAction string

// All supported lifecycle events for network peers.
const (
	NetworkPeerCreated = NetworkForwardAction(api.EventLifecycleNetworkPeerCreated)
	NetworkPeerDeleted = NetworkForwardAction(api.EventLifecycleNetworkPeerDeleted)
	NetworkPeerUpdated = NetworkForwardAction(api.EventLifecycleNetworkPeerUpdated)
)

// Event creates the lifecycle event for an action on a network forward.
func (a NetworkPeerAction) Event(n network, peerName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "peers", peerName).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
