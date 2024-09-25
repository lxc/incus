package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ImageAction represents a lifecycle event action for images.
type ImageAction string

// All supported lifecycle events for images.
const (
	ImageCreated       = ImageAction(api.EventLifecycleImageCreated)
	ImageDeleted       = ImageAction(api.EventLifecycleImageDeleted)
	ImageUpdated       = ImageAction(api.EventLifecycleImageUpdated)
	ImageRetrieved     = ImageAction(api.EventLifecycleImageRetrieved)
	ImageRefreshed     = ImageAction(api.EventLifecycleImageRefreshed)
	ImageSecretCreated = ImageAction(api.EventLifecycleImageSecretCreated)
)

// Event creates the lifecycle event for an action on an image.
func (a ImageAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "images", image).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
