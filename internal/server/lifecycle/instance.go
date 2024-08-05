package lifecycle

import (
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// Internal copy of the instance interface.
type instance interface {
	Name() string
	Project() api.Project
	Operation() *operations.Operation
}

// InstanceAction represents a lifecycle event action for instances.
type InstanceAction string

// All supported lifecycle events for instances.
const (
	InstanceConsole          = InstanceAction(api.EventLifecycleInstanceConsole)
	InstanceConsoleReset     = InstanceAction(api.EventLifecycleInstanceConsoleReset)
	InstanceConsoleRetrieved = InstanceAction(api.EventLifecycleInstanceConsoleRetrieved)
	InstanceCreated          = InstanceAction(api.EventLifecycleInstanceCreated)
	InstanceDeleted          = InstanceAction(api.EventLifecycleInstanceDeleted)
	InstanceExec             = InstanceAction(api.EventLifecycleInstanceExec)
	InstanceFileDeleted      = InstanceAction(api.EventLifecycleInstanceFileDeleted)
	InstanceFilePushed       = InstanceAction(api.EventLifecycleInstanceFilePushed)
	InstanceFileRetrieved    = InstanceAction(api.EventLifecycleInstanceFileRetrieved)
	InstanceMigrated         = InstanceAction(api.EventLifecycleInstanceMigrated)
	InstancePaused           = InstanceAction(api.EventLifecycleInstancePaused)
	InstanceReady            = InstanceAction(api.EventLifecycleInstanceReady)
	InstanceRenamed          = InstanceAction(api.EventLifecycleInstanceRenamed)
	InstanceRestarted        = InstanceAction(api.EventLifecycleInstanceRestarted)
	InstanceRestored         = InstanceAction(api.EventLifecycleInstanceRestored)
	InstanceResumed          = InstanceAction(api.EventLifecycleInstanceResumed)
	InstanceShutdown         = InstanceAction(api.EventLifecycleInstanceShutdown)
	InstanceStarted          = InstanceAction(api.EventLifecycleInstanceStarted)
	InstanceStopped          = InstanceAction(api.EventLifecycleInstanceStopped)
	InstanceUpdated          = InstanceAction(api.EventLifecycleInstanceUpdated)
)

// Event creates the lifecycle event for an action on an instance.
func (a InstanceAction) Event(inst instance, ctx map[string]any) api.EventLifecycle {
	url := api.NewURL().Path(version.APIVersion, "instances", inst.Name()).Project(inst.Project().Name)

	var requestor *api.EventLifecycleRequestor
	if inst.Operation() != nil {
		requestor = inst.Operation().Requestor()
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    url.String(),
		Context:   ctx,
		Requestor: requestor,
		Name:      inst.Name(),
		Project:   inst.Project().Name,
	}
}
