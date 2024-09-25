package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ProfileAction represents a lifecycle event action for profiles.
type ProfileAction string

// All supported lifecycle events for profiles.
const (
	ProfileCreated = ProfileAction(api.EventLifecycleProfileCreated)
	ProfileDeleted = ProfileAction(api.EventLifecycleProfileDeleted)
	ProfileUpdated = ProfileAction(api.EventLifecycleProfileUpdated)
	ProfileRenamed = ProfileAction(api.EventLifecycleProfileRenamed)
)

// Event creates the lifecycle event for an action on a profile.
func (a ProfileAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "profiles", name).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
