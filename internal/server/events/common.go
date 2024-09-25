package events

import (
	"context"
	"sync"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cancel"
	"github.com/lxc/incus/v6/shared/logger"
)

// EventHandler called when the connection receives an event from the client.
type EventHandler func(event api.Event)

// serverCommon represents an instance of a comon event server.
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
}

func (e *listenerCommon) start() {
	logger.Debug("Event listener server handler started", logger.Ctx{"id": e.id, "local": e.LocalAddr(), "remote": e.RemoteAddr()})

	e.Reader(e.done.Context, e.recvFunc)
	e.Close()
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
