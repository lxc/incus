package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// InstanceMetadataAction represents a lifecycle event action for instance metadata.
type InstanceMetadataAction string

// All supported lifecycle events for instance metadata.
const (
	InstanceMetadataUpdated   = InstanceMetadataAction(api.EventLifecycleInstanceMetadataUpdated)
	InstanceMetadataRetrieved = InstanceMetadataAction(api.EventLifecycleInstanceMetadataRetrieved)
)

// Event creates the lifecycle event for an action on instance metadata.
func (a InstanceMetadataAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "instances", inst.Name(), "metadata").Project(inst.Project().Name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
