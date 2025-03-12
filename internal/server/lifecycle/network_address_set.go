package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkAddressSet ia an internal copy of the network address set interface.
type NetworkAddressSet interface {
	Info() *api.NetworkAddressSet
	Project() string
}

// NetworkAddressSetAction represents a lifecycle event action for network address sets.
type NetworkAddressSetAction string

// All supported lifecycle events for network address sets.
const (
	NetworkAddressSetCreated = NetworkAddressSetAction(api.EventLifecycleNetworkAddressSetCreated)
	NetworkAddressSetDeleted = NetworkAddressSetAction(api.EventLifecycleNetworkAddressSetDeleted)
	NetworkAddressSetUpdated = NetworkAddressSetAction(api.EventLifecycleNetworkAddressSetUpdated)
	NetworkAddressSetRenamed = NetworkAddressSetAction(api.EventLifecycleNetworkAddressSetRenamed)
)

// Event creates the lifecycle event for an action on a network acl.
func (a NetworkAddressSetAction) Event(n NetworkAddressSet, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-address-sets", n.Info().Name).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
