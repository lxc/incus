package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkIntegrationAction represents a lifecycle event action for network integrations.
type NetworkIntegrationAction string

// All supported lifecycle events for network integrations.
const (
	NetworkIntegrationCreated = NetworkIntegrationAction(api.EventLifecycleNetworkIntegrationCreated)
	NetworkIntegrationDeleted = NetworkIntegrationAction(api.EventLifecycleNetworkIntegrationDeleted)
	NetworkIntegrationUpdated = NetworkIntegrationAction(api.EventLifecycleNetworkIntegrationUpdated)
	NetworkIntegrationRenamed = NetworkIntegrationAction(api.EventLifecycleNetworkIntegrationRenamed)
)

// Event creates the lifecycle event for an action on a network integration.
func (a NetworkIntegrationAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-integrations", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
