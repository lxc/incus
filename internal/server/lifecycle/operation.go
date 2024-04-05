package lifecycle

import (
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the operation interface.
type operation interface {
	ID() string
}

// OperationAction represents a lifecycle event action for operations.
type OperationAction string

// All supported lifecycle events for operations.
const (
	OperationCancelled = OperationAction(api.EventLifecycleOperationCancelled)
)

// Event creates the lifecycle event for an action on an operation.
func (a OperationAction) Event(op operation, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "operations", op.ID())

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
