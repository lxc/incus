package incus

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/lxc/incus/v7/shared/api"
)

// eventChannelSize is the buffer size used for channels added without an explicit size.
const eventChannelSize = 1000

// The EventListener struct is used to interact with an Incus event stream.
type EventListener struct {
	r         *ProtocolIncus
	ctx       context.Context
	ctxCancel context.CancelFunc
	err       error

	// projectName stores which project this event listener is associated with (empty for all projects).
	projectName string
	targets     []*EventTarget
	channels    []*eventChannel
	targetsLock sync.Mutex
}

// The EventTarget struct is returned to the caller of AddHandler and used in RemoveHandler.
type EventTarget struct {
	function func(api.Event)
	types    []string
}

// The eventChannel struct tracks a channel added through AddChannel.
type eventChannel struct {
	ch    chan api.Event
	types []string
}

// send passes an event on to the handlers and channels of this listener.
func (e *EventListener) send(event api.Event) {
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	if e.ctx.Err() != nil {
		return
	}

	for _, target := range e.targets {
		if target.types != nil && !slices.Contains(target.types, event.Type) {
			continue
		}

		go target.function(event)
	}

	for _, entry := range e.channels {
		if entry.types != nil && !slices.Contains(entry.types, event.Type) {
			continue
		}

		select {
		case entry.ch <- event:
		default:
			// Dropping events would leave the reader with a silently incomplete view.
			e.err = errors.New("Event channel is too far behind")
			e.ctxCancel()

			return
		}
	}
}

// AddChannel adds a channel to be sent every matching event, size 0 means use the client's default.
func (e *EventListener) AddChannel(types []string, size int) <-chan api.Event {
	if size <= 0 {
		size = eventChannelSize
	}

	ch := make(chan api.Event, size)

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// A listener that is already done will never deliver anything.
	if e.ctx.Err() != nil {
		close(ch)

		return ch
	}

	// Close the channels once the listener is done so that readers can range over them.
	if e.channels == nil {
		context.AfterFunc(e.ctx, e.closeChannels)
	}

	e.channels = append(e.channels, &eventChannel{ch: ch, types: types})

	return ch
}

// RemoveChannel removes and closes a channel previously added with AddChannel.
func (e *EventListener) RemoveChannel(ch <-chan api.Event) error {
	if ch == nil {
		return errors.New("A valid channel must be provided")
	}

	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// Locate and remove the channel from the list
	for i, entry := range e.channels {
		if entry.ch == ch {
			close(entry.ch)
			copy(e.channels[i:], e.channels[i+1:])
			e.channels[len(e.channels)-1] = nil
			e.channels = e.channels[:len(e.channels)-1]

			return nil
		}
	}

	return errors.New("Couldn't find this channel")
}

// closeChannels closes all remaining channels once the listener is done.
func (e *EventListener) closeChannels() {
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	for _, entry := range e.channels {
		close(entry.ch)
	}

	e.channels = nil
}

// AddHandler adds a function to be called whenever an event is received.
func (e *EventListener) AddHandler(types []string, function func(api.Event)) (*EventTarget, error) {
	if function == nil {
		return nil, errors.New("A valid function must be provided")
	}

	// Create a new target
	target := &EventTarget{
		function: function,
		types:    types,
	}

	e.addTarget(target)

	return target, nil
}

// addTarget adds an existing target to the listener.
func (e *EventListener) addTarget(target *EventTarget) {
	// Handle locking
	e.targetsLock.Lock()
	defer e.targetsLock.Unlock()

	// And add it to the targets
	e.targets = append(e.targets, target)
}

// RemoveHandler removes a function to be called whenever an event is received.
func (e *EventListener) RemoveHandler(target *EventTarget) error {
	if target == nil {
		return errors.New("A valid event target must be provided")
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

	return errors.New("Couldn't find this function and event types combination")
}

// Disconnect must be used once done listening for events.
func (e *EventListener) Disconnect() {
	// Handle locking
	e.r.eventListenersLock.Lock()
	defer e.r.eventListenersLock.Unlock()

	// Locate and remove it from the global list
	for i, listener := range e.r.eventListeners[e.projectName] {
		if listener == e {
			copy(e.r.eventListeners[e.projectName][i:], e.r.eventListeners[e.projectName][i+1:])
			e.r.eventListeners[e.projectName][len(e.r.eventListeners[e.projectName])-1] = nil
			e.r.eventListeners[e.projectName] = e.r.eventListeners[e.projectName][:len(e.r.eventListeners[e.projectName])-1]
			break
		}
	}

	if e.ctx.Err() != nil {
		return
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
