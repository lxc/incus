package lifecycle

import (
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/version"
)

// InstanceLogAction represents a lifecycle event action for instance logs.
type InstanceLogAction string

// All supported lifecycle events for instance logs.
const (
	InstanceLogRetrieved = InstanceLogAction(api.EventLifecycleInstanceLogRetrieved)
	InstanceLogDeleted   = InstanceLogAction(api.EventLifecycleInstanceLogDeleted)
)

// Event creates the lifecycle event for an action on an instance log.
func (a InstanceLogAction) Event(file string, inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "instances", inst.Name(), "backups", file).Project(inst.Project().Name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
