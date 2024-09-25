package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the network zone interface.
type networkZone interface {
	Info() *api.NetworkZone
	Project() string
}

// NetworkZoneAction represents a lifecycle event action for network zones.
type NetworkZoneAction string

// NetworkZoneRecordAction represents a lifecycle event action for network zone records.
type NetworkZoneRecordAction string

// All supported lifecycle events for network zones.
const (
	NetworkZoneCreated = NetworkZoneAction(api.EventLifecycleNetworkZoneCreated)
	NetworkZoneDeleted = NetworkZoneAction(api.EventLifecycleNetworkZoneDeleted)
	NetworkZoneUpdated = NetworkZoneAction(api.EventLifecycleNetworkZoneUpdated)

	NetworkZoneRecordCreated = NetworkZoneRecordAction(api.EventLifecycleNetworkZoneRecordCreated)
	NetworkZoneRecordDeleted = NetworkZoneRecordAction(api.EventLifecycleNetworkZoneRecordDeleted)
	NetworkZoneRecordUpdated = NetworkZoneRecordAction(api.EventLifecycleNetworkZoneRecordUpdated)
)

// Event creates the lifecycle event for an action on a network zone.
func (a NetworkZoneAction) Event(n networkZone, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-zones", n.Info().Name).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}

// Event creates the lifecycle event for an action on a network zone record.
func (a NetworkZoneRecordAction) Event(n networkZone, name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-zones", n.Info().Name, "records", name).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
