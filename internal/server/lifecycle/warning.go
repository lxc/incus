package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// WarningAction represents a lifecycle event action for warnings.
type WarningAction string

// All supported lifecycle events for warnings.
const (
	WarningAcknowledged = WarningAction(api.EventLifecycleWarningAcknowledged)
	WarningReset        = WarningAction(api.EventLifecycleWarningReset)
	WarningDeleted      = WarningAction(api.EventLifecycleWarningDeleted)
)

// Event creates the lifecycle event for an action on a warning.
func (a WarningAction) Event(id string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "warnings", id)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
