package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// InstanceMetadataTemplateAction represents a lifecycle event action for instance metadata templates.
type InstanceMetadataTemplateAction string

// All supported lifecycle events for instance metadata templates.
const (
	InstanceMetadataTemplateDeleted   = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateDeleted)
	InstanceMetadataTemplateCreated   = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateCreated)
	InstanceMetadataTemplateRetrieved = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateRetrieved)
)

// Event creates the lifecycle event for an action on instance metadata templates.
func (a InstanceMetadataTemplateAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "instances", inst.Name(), "metadata", "templates").Project(inst.Project().Name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
