package operations

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/events"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/cancel"
	"github.com/lxc/incus/shared/logger"
)

var debug bool

var operationsLock sync.Mutex
var operations = make(map[string]*Operation)

// OperationClass represents the OperationClass type.
type OperationClass int

const (
	// OperationClassTask represents the Task OperationClass.
	OperationClassTask OperationClass = 1
	// OperationClassWebsocket represents the Websocket OperationClass.
	OperationClassWebsocket OperationClass = 2
	// OperationClassToken represents the Token OperationClass.
	OperationClassToken OperationClass = 3
)

func (t OperationClass) String() string {
	return map[OperationClass]string{
		OperationClassTask:      api.OperationClassTask,
		OperationClassWebsocket: api.OperationClassWebsocket,
		OperationClassToken:     api.OperationClassToken,
	}[t]
}

// Init sets the debug value for the operations package.
func Init(d bool) {
	debug = d
}

// Lock locks the operations mutex.
func Lock() {
	operationsLock.Lock()
}

// Unlock unlocks the operations mutex.
func Unlock() {
	operationsLock.Unlock()
}

// Clone returns a clone of the internal operations map containing references to the actual operations.
func Clone() map[string]*Operation {
	operationsLock.Lock()
	defer operationsLock.Unlock()

	localOperations := make(map[string]*Operation, len(operations))
	for k, v := range operations {
		localOperations[k] = v
	}

	return localOperations
}

// OperationGetInternal returns the operation with the given id. It returns an
// error if it doesn't exist.
func OperationGetInternal(id string) (*Operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

// Operation represents an operation.
type Operation struct {
	projectName string
	id          string
	class       OperationClass
	createdAt   time.Time
	updatedAt   time.Time
	status      api.StatusCode
	url         string
	resources   map[string][]api.URL
	metadata    map[string]any
	err         error
	readonly    bool
	canceler    *cancel.HTTPRequestCanceller
	description string
	objectType  auth.ObjectType
	entitlement auth.Entitlement
	dbOpType    operationtype.Type
	requestor   *api.EventLifecycleRequestor
	logger      logger.Logger

	// Those functions are called at various points in the Operation lifecycle
	onRun     func(*Operation) error
	onCancel  func(*Operation) error
	onConnect func(*Operation, *http.Request, http.ResponseWriter) error

	// Indicates if operation has finished.
	finished *cancel.Canceller

	// Locking for concurent access to the Operation
	lock sync.Mutex

	state  *state.State
	events *events.Server
}

// OperationCreate creates a new operation and returns it. If it cannot be
// created, it returns an error.
func OperationCreate(s *state.State, projectName string, opClass OperationClass, opType operationtype.Type, opResources map[string][]api.URL, opMetadata any, onRun func(*Operation) error, onCancel func(*Operation) error, onConnect func(*Operation, *http.Request, http.ResponseWriter) error, r *http.Request) (*Operation, error) {
	// Don't allow new operations when Incus is shutting down.
	if s != nil && s.ShutdownCtx.Err() == context.Canceled {
		return nil, fmt.Errorf("Incus is shutting down")
	}

	// Main attributes
	op := Operation{}
	op.projectName = projectName
	op.id = uuid.New().String()
	op.description = opType.Description()
	op.objectType, op.entitlement = opType.Permission()
	op.dbOpType = opType
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", version.APIVersion, op.id)
	op.resources = opResources
	op.finished = cancel.New(context.Background())
	op.state = s
	op.logger = logger.AddContext(logger.Ctx{"operation": op.id, "project": op.projectName, "class": op.class.String(), "description": op.description})

	if s != nil {
		op.SetEventServer(s.Events)
	}

	newMetadata, err := parseMetadata(opMetadata)
	if err != nil {
		return nil, err
	}

	op.metadata = newMetadata

	// Callback functions
	op.onRun = onRun
	op.onCancel = onCancel
	op.onConnect = onConnect

	// Quick check.
	if op.class != OperationClassWebsocket && op.onConnect != nil {
		return nil, fmt.Errorf("Only websocket operations can have a Connect hook")
	}

	if op.class == OperationClassWebsocket && op.onConnect == nil {
		return nil, fmt.Errorf("Websocket operations must have a Connect hook")
	}

	if op.class == OperationClassToken && op.onRun != nil {
		return nil, fmt.Errorf("Token operations can't have a Run hook")
	}

	if op.class == OperationClassToken && op.onCancel != nil {
		return nil, fmt.Errorf("Token operations can't have a Cancel hook")
	}

	// Set requestor if request was provided.
	if r != nil {
		op.SetRequestor(r)
	}

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	err = registerDBOperation(&op, opType)
	if err != nil {
		return nil, err
	}

	op.logger.Debug("New operation")
	_, md, _ := op.Render()

	operationsLock.Lock()
	op.sendEvent(md)
	operationsLock.Unlock()

	return &op, nil
}

// SetEventServer allows injection of event server.
func (op *Operation) SetEventServer(events *events.Server) {
	op.events = events
}

// SetRequestor sets a requestor for this operation from an http.Request.
func (op *Operation) SetRequestor(r *http.Request) {
	op.requestor = request.CreateRequestor(r)
}

// Requestor returns the initial requestor for this operation.
func (op *Operation) Requestor() *api.EventLifecycleRequestor {
	return op.requestor
}

func (op *Operation) done() {
	if op.readonly {
		return
	}

	op.lock.Lock()
	op.readonly = true
	op.onRun = nil
	op.onCancel = nil
	op.onConnect = nil
	op.finished.Cancel()
	op.lock.Unlock()

	go func() {
		shutdownCtx := context.Background()
		if op.state != nil {
			shutdownCtx = op.state.ShutdownCtx
		}

		select {
		case <-shutdownCtx.Done():
			return // Expect all operation records to be removed by waitForOperations in one query.
		case <-time.After(time.Second * 5): // Wait 5s before removing from internal map and database.
		}

		operationsLock.Lock()
		_, ok := operations[op.id]
		if !ok {
			operationsLock.Unlock()
			return
		}

		delete(operations, op.id)
		operationsLock.Unlock()

		if op.state == nil {
			return
		}

		err := removeDBOperation(op)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			// Operations can be deleted from the database before the operation clean up go routine has
			// run in cases where the project that the operation(s) are associated to is deleted first.
			// So don't log warning if operation not found.
			op.logger.Warn("Failed to delete operation", logger.Ctx{"status": op.status, "err": err})
		}
	}()
}

// Start a pending operation. It returns an error if the operation cannot be started.
func (op *Operation) Start() error {
	op.lock.Lock()
	if op.status != api.Pending {
		op.lock.Unlock()
		return fmt.Errorf("Only pending operations can be started")
	}

	op.status = api.Running

	if op.onRun != nil {
		go func(op *Operation) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = api.Failure
				op.err = err
				op.lock.Unlock()
				op.done()

				op.logger.Debug("Failure for operation", logger.Ctx{"err": err})
				_, md, _ := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			op.status = api.Success
			op.lock.Unlock()
			op.done()

			op.logger.Debug("Success for operation")
			_, md, _ := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()
		}(op)
	}

	op.lock.Unlock()

	op.logger.Debug("Started operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// Cancel cancels a running operation. If the operation cannot be cancelled, it
// returns an error.
func (op *Operation) Cancel() (chan error, error) {
	op.lock.Lock()
	if op.status != api.Running {
		op.lock.Unlock()
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.mayCancel() {
		op.lock.Unlock()
		return nil, fmt.Errorf("This operation can't be cancelled")
	}

	chanCancel := make(chan error, 1)

	oldStatus := op.status
	op.status = api.Cancelling
	op.lock.Unlock()

	hasOnCancel := op.onCancel != nil

	if hasOnCancel {
		go func(op *Operation, oldStatus api.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				op.logger.Debug("Failed to cancel operation", logger.Ctx{"err": err})
				_, md, _ := op.Render()

				op.lock.Lock()
				op.sendEvent(md)
				op.lock.Unlock()

				return
			}

			op.lock.Lock()
			op.status = api.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			op.logger.Debug("Cancelled operation")
			_, md, _ := op.Render()

			op.lock.Lock()
			op.sendEvent(md)
			op.lock.Unlock()
		}(op, oldStatus, chanCancel)
	}

	op.logger.Debug("Cancelling operation")
	_, md, _ := op.Render()
	op.sendEvent(md)

	if op.canceler != nil {
		err := op.canceler.Cancel()
		if err != nil {
			return nil, err
		}
	}

	if !hasOnCancel {
		op.lock.Lock()
		op.status = api.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	op.logger.Debug("Cancelled operation")
	_, md, _ = op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return chanCancel, nil
}

// Connect connects a websocket operation. If the operation is not a websocket
// operation or the operation is not running, it returns an error.
func (op *Operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	op.lock.Lock()
	if op.class != OperationClassWebsocket {
		op.lock.Unlock()
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != api.Running {
		op.lock.Unlock()
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	go func(op *Operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			op.logger.Debug("Failed to connect to operation", logger.Ctx{"err": err})
			return
		}

		chanConnect <- nil

		op.logger.Debug("Connected to operation")
	}(op, chanConnect)
	op.lock.Unlock()

	op.logger.Debug("Connecting to operation")

	return chanConnect, nil
}

func (op *Operation) mayCancel() bool {
	if op.class == OperationClassToken {
		return true
	}

	if op.onCancel != nil {
		return true
	}

	if op.canceler != nil && op.canceler.Cancelable() {
		return true
	}

	return false
}

// Render renders the operation structure.
// Returns URL of operation and operation info.
func (op *Operation) Render() (string, *api.Operation, error) {
	// Setup the resource URLs
	renderedResources := make(map[string][]string)
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values []string
			for _, c := range value {
				values = append(values, c.Project(op.Project()).String())
			}

			tmpResources[key] = values
		}

		renderedResources = tmpResources
	}

	// Local server name

	op.lock.Lock()
	retOp := &api.Operation{
		ID:          op.id,
		Class:       op.class.String(),
		Description: op.description,
		CreatedAt:   op.createdAt,
		UpdatedAt:   op.updatedAt,
		Status:      op.status.String(),
		StatusCode:  op.status,
		Resources:   renderedResources,
		Metadata:    op.metadata,
		MayCancel:   op.mayCancel(),
	}

	if op.state != nil {
		retOp.Location = op.state.ServerName
	}

	if op.err != nil {
		retOp.Err = response.SmartError(op.err).String()
	}

	op.lock.Unlock()

	return op.url, retOp, nil
}

// Wait for the operation to be done.
// Returns non-nil error if operation failed or context was cancelled.
func (op *Operation) Wait(ctx context.Context) error {
	select {
	case <-op.finished.Done():
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// UpdateResources updates the resources of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateResources(opResources map[string][]api.URL) error {
	op.lock.Lock()
	if op.status != api.Pending && op.status != api.Running {
		op.lock.Unlock()
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		op.lock.Unlock()
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.updatedAt = time.Now()
	op.resources = opResources
	op.lock.Unlock()

	op.logger.Debug("Updated resources for oeration")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// UpdateMetadata updates the metadata of the operation. It returns an error
// if the operation is not pending or running, or the operation is read-only.
func (op *Operation) UpdateMetadata(opMetadata any) error {
	op.lock.Lock()
	if op.status != api.Pending && op.status != api.Running {
		op.lock.Unlock()
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		op.lock.Unlock()
		return fmt.Errorf("Read-only operations can't be updated")
	}

	newMetadata, err := parseMetadata(opMetadata)
	if err != nil {
		return err
	}

	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// ExtendMetadata updates the metadata of the operation with the additional data provided.
// It returns an error if the operation is not pending or running, or the operation is read-only.
func (op *Operation) ExtendMetadata(metadata any) error {
	op.lock.Lock()

	// Quick checks.
	if op.status != api.Pending && op.status != api.Running {
		op.lock.Unlock()
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		op.lock.Unlock()
		return fmt.Errorf("Read-only operations can't be updated")
	}

	// Parse the new metadata.
	extraMetadata, err := parseMetadata(metadata)
	if err != nil {
		return err
	}

	// Get current metadata.
	newMetadata := op.metadata
	op.lock.Unlock()

	// Merge with current one.
	if op.metadata == nil {
		newMetadata = extraMetadata
	} else {
		for k, v := range extraMetadata {
			newMetadata[k] = v
		}
	}

	// Update the operation.
	op.lock.Lock()
	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	op.logger.Debug("Updated metadata for operation")
	_, md, _ := op.Render()

	op.lock.Lock()
	op.sendEvent(md)
	op.lock.Unlock()

	return nil
}

// ID returns the operation ID.
func (op *Operation) ID() string {
	return op.id
}

// Metadata returns the operation Metadata.
func (op *Operation) Metadata() map[string]any {
	return op.metadata
}

// URL returns the operation URL.
func (op *Operation) URL() string {
	return op.url
}

// Resources returns the operation resources.
func (op *Operation) Resources() map[string][]api.URL {
	return op.resources
}

// SetCanceler sets a canceler.
func (op *Operation) SetCanceler(canceler *cancel.HTTPRequestCanceller) {
	op.canceler = canceler
}

// Permission returns the operations auth.ObjectType and auth.Entitlement.
func (op *Operation) Permission() (auth.ObjectType, auth.Entitlement) {
	return op.objectType, op.entitlement
}

// Project returns the operation project.
func (op *Operation) Project() string {
	return op.projectName
}

// Status returns the operation status.
func (op *Operation) Status() api.StatusCode {
	return op.status
}

// Class returns the operation class.
func (op *Operation) Class() OperationClass {
	return op.class
}

// Type returns the db operation type.
func (op *Operation) Type() operationtype.Type {
	return op.dbOpType
}
