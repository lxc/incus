package incus

import (
	"context"
	"fmt"
	"sync"

	"github.com/lxc/incus/shared/api"
)

// The EventListener struct is used to interact with an Incus event stream.
type EventListener struct {
	r         *ProtocolIncus
	ctx       context.Context
	ctxCancel context.CancelFunc
	err       error

	// projectName stores which project this event listener is associated with (empty for all projects).
	projectName string
	targets     []*EventTarget
	targetsLock sync.Mutex
}

// The EventTarget struct is returned to the caller of AddHandler and used in RemoveHandler.
type EventTarget struct {
	function func(api.Event)
	types    []string
}

// AddHandler adds a function to be called whenever an event is received.
func (e *EventListener) AddHandler(types []string, function func(api.Event)) (*EventTarget, error) {
	if function == nil {
		return nil, fmt.Errorf("A valid function must be provided")
	}

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// Create a new target
	target := EventTarget{
		function: function,
		types:    types,
	}

	// And add it to the targets
	e.targets = append(e.targets, &target)

	return &target, nil
}

// RemoveHandler removes a function to be called whenever an event is received.
func (e *EventListener) RemoveHandler(target *EventTarget) error {
	if target == nil {
		return fmt.Errorf("A valid event target must be provided")
	}

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// Locate and remove the function from the list
	for i, entry := range e.targets {
		if entry == target {
			copy(e.targets[i:], e.targets[i+1:])
			e.targets[len(e.targets)-1] = nil
			e.targets = e.targets[:len(e.targets)-1]
			return nil
		}
	}

	return fmt.Errorf("Couldn't find this function and event types combination")
}

// Disconnect must be used once done listening for events.
func (e *EventListener) Disconnect() {
	// Handle locking
	e.r.eventListenersLock.Lock()
	defer e.r.eventListenersLock.Unlock()

	if e.ctx.Err() != nil {
		return
	}

	// Locate and remove it from the global list
	for i, listener := range e.r.eventListeners[e.projectName] {
		if listener == e {
			copy(e.r.eventListeners[e.projectName][i:], e.r.eventListeners[e.projectName][i+1:])
			e.r.eventListeners[e.projectName][len(e.r.eventListeners[e.projectName])-1] = nil
			e.r.eventListeners[e.projectName] = e.r.eventListeners[e.projectName][:len(e.r.eventListeners[e.projectName])-1]
			break
		}
	}

	// Turn off the handler
	e.err = nil
	e.ctxCancel()
}

// Wait blocks until the server disconnects the connection or Disconnect() is called.
func (e *EventListener) Wait() error {
	<-e.ctx.Done()
	return e.err
}

// IsActive returns true if this listener is still connected, false otherwise.
func (e *EventListener) IsActive() bool {
	return e.ctx.Err() == nil
}
