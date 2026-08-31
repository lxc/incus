package lifecycle

import (
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
)

// NetworkPeerGroupAction represents a lifecycle event action for network peer groups.
type NetworkPeerGroupAction string

// All supported lifecycle events for network peer groups.
const (
	NetworkPeerGroupCreated = NetworkPeerGroupAction(api.EventLifecycleNetworkPeerGroupCreated)
	NetworkPeerGroupDeleted = NetworkPeerGroupAction(api.EventLifecycleNetworkPeerGroupDeleted)
	NetworkPeerGroupUpdated = NetworkPeerGroupAction(api.EventLifecycleNetworkPeerGroupUpdated)
)

// Event creates the lifecycle event for an action on a network peer group.
func (a NetworkPeerGroupAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-peer-groups", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
