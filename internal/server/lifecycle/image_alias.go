package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ImageAliasAction represents a lifecycle event action for image aliases.
type ImageAliasAction string

// All supported lifecycle events for image aliases.
const (
	ImageAliasCreated = ImageAliasAction(api.EventLifecycleImageAliasCreated)
	ImageAliasDeleted = ImageAliasAction(api.EventLifecycleImageAliasDeleted)
	ImageAliasUpdated = ImageAliasAction(api.EventLifecycleImageAliasUpdated)
	ImageAliasRenamed = ImageAliasAction(api.EventLifecycleImageAliasRenamed)
)

// Event creates the lifecycle event for an action on an image alias.
func (a ImageAliasAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "images", "aliases", image).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
