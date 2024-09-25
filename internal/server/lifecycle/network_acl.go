package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the network acl interface.
type networkACL interface {
	Info() *api.NetworkACL
	Project() string
}

// NetworkACLAction represents a lifecycle event action for network acls.
type NetworkACLAction string

// All supported lifecycle events for network acls.
const (
	NetworkACLCreated = NetworkACLAction(api.EventLifecycleNetworkACLCreated)
	NetworkACLDeleted = NetworkACLAction(api.EventLifecycleNetworkACLDeleted)
	NetworkACLUpdated = NetworkACLAction(api.EventLifecycleNetworkACLUpdated)
	NetworkACLRenamed = NetworkACLAction(api.EventLifecycleNetworkACLRenamed)
)

// Event creates the lifecycle event for an action on a network acl.
func (a NetworkACLAction) Event(n networkACL, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "network-acls", n.Info().Name).Project(n.Project())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
