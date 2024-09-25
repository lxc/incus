package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkLoadBalancerAction represents a lifecycle event action for network load balancers.
type NetworkLoadBalancerAction string

// All supported lifecycle events for network load balancers.
const (
	NetworkLoadBalancerCreated = NetworkLoadBalancerAction(api.EventLifecycleNetworkLoadBalancerCreated)
	NetworkLoadBalancerDeleted = NetworkLoadBalancerAction(api.EventLifecycleNetworkLoadBalancerDeleted)
	NetworkLoadBalancerUpdated = NetworkLoadBalancerAction(api.EventLifecycleNetworkLoadBalancerUpdated)
)

// Event creates the lifecycle event for an action on a network load balancer.
func (a NetworkLoadBalancerAction) Event(n network, listenAddress string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "load-balancers", listenAddress).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
