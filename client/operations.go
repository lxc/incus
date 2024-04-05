package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
)

// The Operation type represents an ongoing Incus operation (asynchronous processing).
type operation struct {
	api.Operation

	r            *ProtocolIncus
	listener     *EventListener
	handlerReady bool
	handlerLock  sync.Mutex
	skipListener bool

	chActive chan bool
}

// AddHandler adds a function to be called whenever an event is received.
func (op *operation) AddHandler(function func(api.Operation)) (*EventTarget, error) {
	if op.skipListener {
		return nil, fmt.Errorf("Cannot add handler, client operation does not support event listeners")
	}

	// Make sure we have a listener setup
	err := op.setupListener()
	if err != nil {
		return nil, err
	}

	// Make sure we're not racing with ourselves
	op.handlerLock.Lock()
	defer op.handlerLock.Unlock()

	// If we're done already, just return
	if op.StatusCode.IsFinal() {
		return nil, nil
	}

	// Wrap the function to filter unwanted messages
	wrapped := func(event api.Event) {
		op.handlerLock.Lock()

		newOp := api.Operation{}
		err := json.Unmarshal(event.Metadata, &newOp)
		if err != nil || newOp.ID != op.ID {
			op.handlerLock.Unlock()
			return
		}

		op.handlerLock.Unlock()

		function(newOp)
	}

	return op.listener.AddHandler([]string{"operation"}, wrapped)
}

// Cancel will request that Incus cancels the operation (if supported).
func (op *operation) Cancel() error {
	return op.r.DeleteOperation(op.ID)
}

// Get returns the API operation struct.
func (op *operation) Get() api.Operation {
	return op.Operation
}

// GetWebsocket returns a raw websocket connection from the operation.
func (op *operation) GetWebsocket(secret string) (*websocket.Conn, error) {
	return op.r.GetOperationWebsocket(op.ID, secret)
}

// RemoveHandler removes a function to be called whenever an event is received.
func (op *operation) RemoveHandler(target *EventTarget) error {
	if op.skipListener {
		return fmt.Errorf("Cannot remove handler, client operation does not support event listeners")
	}

	// Make sure we're not racing with ourselves
	op.handlerLock.Lock()
	defer op.handlerLock.Unlock()

	// If the listener is gone, just return
	if op.listener == nil {
		return nil
	}

	return op.listener.RemoveHandler(target)
}

// Refresh pulls the current version of the operation and updates the struct.
func (op *operation) Refresh() error {
	// Get the current version of the operation
	newOp, _, err := op.r.GetOperation(op.ID)
	if err != nil {
		return err
	}

	// Update the operation struct
	op.Operation = *newOp

	return nil
}

// Wait lets you wait until the operation reaches a final state.
func (op *operation) Wait() error {
	return op.WaitContext(context.Background())
}

// WaitContext lets you wait until the operation reaches a final state with context.Context.
func (op *operation) WaitContext(ctx context.Context) error {
	if op.skipListener {
		timeout := -1
		deadline, ok := ctx.Deadline()
		if ok {
			timeout = int(time.Until(deadline).Seconds())
		}

		opAPI, _, err := op.r.GetOperationWait(op.ID, timeout)
		if err != nil {
			return err
		}

		op.Operation = *opAPI

		if opAPI.Err != "" {
			return errors.New(opAPI.Err)
		}

		return nil
	}

	op.handlerLock.Lock()
	// Check if not done already
	if op.StatusCode.IsFinal() {
		if op.Err != "" {
			op.handlerLock.Unlock()
			return errors.New(op.Err)
		}

		op.handlerLock.Unlock()
		return nil
	}

	op.handlerLock.Unlock()

	// Make sure we have a listener setup
	err := op.setupListener()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-op.chActive:
	}

	// We're done, parse the result
	if op.Err != "" {
		return errors.New(op.Err)
	}

	return nil
}

// setupListener initiates an event listener for an operation and manages updates to the operation's state.
// It adds handlers to process events, monitors the listener for completion or errors,
// and triggers a manual refresh of the operation's state to prevent race conditions.
func (op *operation) setupListener() error {
	if op.skipListener {
		return fmt.Errorf("Cannot set up event listener, client operation does not support event listeners")
	}

	// Make sure we're not racing with ourselves
	op.handlerLock.Lock()
	defer op.handlerLock.Unlock()

	// We already have a listener setup
	if op.handlerReady {
		return nil
	}

	op.handlerReady = true

	// Get a new listener
	if op.listener == nil {
		listener, err := op.r.GetEvents()
		if err != nil {
			return err
		}

		op.listener = listener
	}

	// Setup the handler
	chReady := make(chan bool)
	_, err := op.listener.AddHandler([]string{"operation"}, func(event api.Event) {
		<-chReady

		// We don't want concurrency while processing events
		op.handlerLock.Lock()
		defer op.handlerLock.Unlock()

		// Check if we're done already (because of another event)
		if op.listener == nil {
			return
		}

		// Get an operation struct out of this data
		newOp := api.Operation{}
		err := json.Unmarshal(event.Metadata, &newOp)
		if err != nil || newOp.ID != op.ID {
			return
		}

		// Update the struct
		op.Operation = newOp

		// And check if we're done
		if op.StatusCode.IsFinal() {
			op.listener.Disconnect()
			op.listener = nil
			close(op.chActive)
			return
		}
	})
	if err != nil {
		op.listener.Disconnect()
		op.listener = nil
		close(op.chActive)
		close(chReady)

		return err
	}

	// Monitor event listener
	go func() {
		<-chReady

		// We don't want concurrency while accessing the listener
		op.handlerLock.Lock()

		// Check if we're done already (because of another event)
		listener := op.listener
		if listener == nil {
			op.handlerLock.Unlock()
			return
		}

		op.handlerLock.Unlock()

		// Wait for the listener or operation to be done
		select {
		case <-listener.ctx.Done():
			op.handlerLock.Lock()
			if op.listener != nil {
				op.Err = listener.err.Error()
				close(op.chActive)
			}

			op.handlerLock.Unlock()
		case <-op.chActive:
			return
		}
	}()

	// And do a manual refresh to avoid races
	err = op.Refresh()
	if err != nil {
		op.listener.Disconnect()
		op.listener = nil
		close(op.chActive)
		close(chReady)

		return err
	}

	// Check if not done already
	if op.StatusCode.IsFinal() {
		op.listener.Disconnect()
		op.listener = nil
		close(op.chActive)
		close(chReady)

		if op.Err != "" {
			return errors.New(op.Err)
		}

		return nil
	}

	// Start processing background updates
	close(chReady)

	return nil
}

// The remoteOperation type represents an ongoing Incus operation between two servers.
type remoteOperation struct {
	targetOp Operation

	handlers    []func(api.Operation)
	handlerLock sync.Mutex

	chDone chan bool
	chPost chan bool
	err    error
}

// AddHandler adds a function to be called whenever an event is received.
func (op *remoteOperation) AddHandler(function func(api.Operation)) (*EventTarget, error) {
	var err error
	var target *EventTarget

	op.handlerLock.Lock()
	defer op.handlerLock.Unlock()

	// Attach to the existing target operation
	if op.targetOp != nil {
		target, err = op.targetOp.AddHandler(function)
		if err != nil {
			return nil, err
		}
	} else {
		// Generate a mock EventTarget
		target = &EventTarget{
			function: func(api.Event) { function(api.Operation{}) },
			types:    []string{"operation"},
		}
	}

	// Add the handler to our list
	op.handlers = append(op.handlers, function)

	return target, nil
}

// CancelTarget attempts to cancel the target operation.
func (op *remoteOperation) CancelTarget() error {
	if op.targetOp == nil {
		return fmt.Errorf("No associated target operation")
	}

	return op.targetOp.Cancel()
}

// GetTarget returns the target operation.
func (op *remoteOperation) GetTarget() (*api.Operation, error) {
	if op.targetOp == nil {
		return nil, fmt.Errorf("No associated target operation")
	}

	opAPI := op.targetOp.Get()
	return &opAPI, nil
}

// Wait lets you wait until the operation reaches a final state.
func (op *remoteOperation) Wait() error {
	<-op.chDone

	if op.chPost != nil {
		<-op.chPost
	}

	return op.err
}
