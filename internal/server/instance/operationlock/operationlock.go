package operationlock

import (
	"context"
	"fmt"
	"sync"

	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/shared/logger"
)

// Action indicates the operation action type.
type Action string

// ActionCreate for creating an instance.
const ActionCreate Action = "create"

// ActionStart for starting an instance.
const ActionStart Action = "start"

// ActionStop for stopping an instance.
const ActionStop Action = "stop"

// ActionRestart for restarting an instance.
const ActionRestart Action = "restart"

// ActionRestore for restoring an instance.
const ActionRestore Action = "restore"

// ActionUpdate for updating an instance.
const ActionUpdate Action = "update"

// ActionDelete for deleting an instance.
const ActionDelete Action = "delete"

// ErrNonReusuableSucceeded is returned when no operation is created due to having to wait for a matching
// non-reusuable operation that has now completed successfully.
var ErrNonReusuableSucceeded error = fmt.Errorf("A matching non-reusable operation has now succeeded")

var instanceOperationsLock sync.Mutex
var instanceOperations = make(map[string]*InstanceOperation)

// InstanceOperation operation locking.
type InstanceOperation struct {
	action            Action
	chanDone          chan error
	err               error
	projectName       string
	instanceName      string
	reusable          bool
	instanceInitiated bool
}

// Create creates a new operation lock for an Instance if one does not already exist and returns it.
// The lock will be released after TimeoutDefault or when Done() is called, which ever occurs first.
// If createReusuable is set as true then future lock attempts can specify the reuseExisting argument as true
// which will then trigger a reset of the timeout to TimeoutDefault on the existing lock and return it.
func Create(projectName string, instanceName string, action Action, createReusuable bool, reuseExisting bool) (*InstanceOperation, error) {
	if projectName == "" || instanceName == "" {
		return nil, fmt.Errorf("Invalid project or instance name")
	}

	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	opKey := project.Instance(projectName, instanceName)

	op := instanceOperations[opKey]
	if op != nil {
		if op.reusable && reuseExisting {
			logger.Debug("Instance operation lock reused", logger.Ctx{"project": op.projectName, "instance": op.instanceName, "action": op.action, "reusable": op.reusable})

			return op, nil
		}

		return nil, fmt.Errorf("Instance is busy running a %q operation", op.action)
	}

	op = &InstanceOperation{}
	op.projectName = projectName
	op.instanceName = instanceName
	op.action = action
	op.reusable = createReusuable
	op.chanDone = make(chan error)

	instanceOperations[opKey] = op
	logger.Debug("Instance operation lock created", logger.Ctx{"project": op.projectName, "instance": op.instanceName, "action": op.action, "reusable": op.reusable})

	return op, nil
}

// CreateWaitGet is a weird function which does what we happen to want most of the time.
//
// If the instance has an operation of the same type and it's not reusable
// or the caller doesn't want to reuse it, the function will wait and
// indicate that it did so.
//
// If the instance has an existing operation of one of the inheritableActions types, then the operation is returned
// to the user. This allows an operation started in one function/routine to be inherited by another.
//
// If the instance doesn't have an ongoing operation, has an operation of a different type that is not in the
// inheritableActions list or has the right type and is being reused, then this behaves as a Create call.
//
// Returns ErrWaitedForMatching if it waited for a matching operation to finish and it's finished successfully and
// so didn't return create a new operation.
func CreateWaitGet(projectName string, instanceName string, action Action, inheritableActions []Action, createReusuable bool, reuseExisting bool) (*InstanceOperation, error) {
	op := Get(projectName, instanceName)

	// No existing operation, call create.
	if op == nil {
		op, err := Create(projectName, instanceName, action, createReusuable, reuseExisting)
		return op, err
	}

	// Operation action matches but is not reusable or we have been asked not to reuse,
	// so wait and return result.
	if op.action == action && (!reuseExisting || !op.reusable) {
		err := op.Wait(context.Background())
		if err != nil {
			return nil, err
		}

		// The matching operation ended without error, but this means we've not created a new
		// operation for this request, so return a special error indicating this scenario.
		return nil, ErrNonReusuableSucceeded
	}

	// Operation action matches one the inheritable actions, return the operation.
	if op.ActionMatch(inheritableActions...) {
		logger.Debug("Instance operation lock inherited", logger.Ctx{"project": op.projectName, "instance": op.instanceName, "action": op.action, "reusable": op.reusable, "inheritedByAction": action})

		return op, nil
	}

	// Send the rest to Create to try and create a new operation.
	op, err := Create(projectName, instanceName, action, createReusuable, reuseExisting)

	return op, err
}

// Get retrieves an existing lock or returns nil if no lock exists.
func Get(projectName string, instanceName string) *InstanceOperation {
	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	opKey := project.Instance(projectName, instanceName)

	return instanceOperations[opKey]
}

// Action returns operation's action.
func (op *InstanceOperation) Action() Action {
	// This function can be called on a nil struct.
	if op == nil {
		return ""
	}

	return op.action
}

// ActionMatch returns true if operation's action matches one of the matchActions.
func (op *InstanceOperation) ActionMatch(matchActions ...Action) bool {
	for _, matchAction := range matchActions {
		if op.action == matchAction {
			return true
		}
	}

	return false
}

// Wait waits for an operation to finish.
func (op *InstanceOperation) Wait(ctx context.Context) error {
	// This function can be called on a nil struct.
	if op == nil {
		return nil
	}

	select {
	case <-op.chanDone:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done indicates the operation has finished.
func (op *InstanceOperation) Done(err error) {
	// This function can be called on a nil struct.
	if op == nil {
		return
	}

	instanceOperationsLock.Lock()
	defer instanceOperationsLock.Unlock()

	opKey := project.Instance(op.projectName, op.instanceName)

	// Check if already done.
	runningOp, ok := instanceOperations[opKey]
	if !ok || runningOp != op {
		return
	}

	op.err = err
	delete(instanceOperations, opKey) // Delete before closing chanDone.
	close(op.chanDone)
	logger.Debug("Instance operation lock finished", logger.Ctx{"project": op.projectName, "instance": op.instanceName, "action": op.action, "reusable": op.reusable, "err": err})
}

// SetInstanceInitiated sets the instance initiated marker.
func (op *InstanceOperation) SetInstanceInitiated(instanceInitiated bool) {
	// This function can be called on a nil struct.
	if op == nil {
		return
	}

	op.instanceInitiated = instanceInitiated
}

// GetInstanceInitiated gets the instance initiated marker.
func (op *InstanceOperation) GetInstanceInitiated() bool {
	// This function can be called on a nil struct.
	if op == nil {
		return false
	}

	return op.instanceInitiated
}
