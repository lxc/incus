package events

import (
	"context"
	"errors"
	"sync"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cancel"
	"github.com/lxc/incus/v7/shared/logger"
)

// eventQueueSize is how many events may be pending delivery to a listener before it gets dropped.
const eventQueueSize = 1000

// EventHandler called when the connection receives an event from the client.
type EventHandler func(event api.Event)

// serverCommon represents an instance of a common event server.
type serverCommon struct {
	debug   bool
	verbose bool
	lock    sync.Mutex
}

// listenerCommon describes a common event listener.
type listenerCommon struct {
	EventListenerConnection

	messageTypes []string
	done         *cancel.Canceller
	id           string
	lock         sync.Mutex
	recvFunc     EventHandler
	writeQueue   chan api.Event
}

func (e *listenerCommon) start() {
	logger.Debug("Event listener server handler started", logger.Ctx{"id": e.id, "local": e.LocalAddr(), "remote": e.RemoteAddr()})

	go e.writer()

	e.Reader(e.done.Context, e.recvFunc)
	e.Close()
}

// enqueue queues an event for delivery, failing if the listener is too far behind.
func (e *listenerCommon) enqueue(event api.Event) error {
	select {
	case e.writeQueue <- event:
		return nil
	default:
		return errors.New("Event queue is full")
	}
}

// writer delivers queued events one at a time so listeners see them in the order they were generated.
func (e *listenerCommon) writer() {
	for {
		select {
		case <-e.done.Done():
			return
		case event := <-e.writeQueue:
			err := e.WriteJSON(event)
			if err != nil {
				e.Close()
				return
			}
		}
	}
}

// IsClosed returns true if the listener is closed.
func (e *listenerCommon) IsClosed() bool {
	return e.done.Err() != nil
}

// ID returns the listener ID.
func (e *listenerCommon) ID() string {
	return e.id
}

// Wait waits for a message on its active channel or the context is cancelled, then returns.
func (e *listenerCommon) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-e.done.Done():
	}
}

// Close Disconnects the listener.
func (e *listenerCommon) Close() {
	e.lock.Lock()
	defer e.lock.Unlock()

	if e.IsClosed() {
		return
	}

	logger.Debug("Event listener server handler stopped", logger.Ctx{"listener": e.ID(), "local": e.LocalAddr(), "remote": e.RemoteAddr()})

	_ = e.EventListenerConnection.Close()
	e.done.Cancel()
}
