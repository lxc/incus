package events

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/lxc/incus/internal/server/storage/memorypipe"
	"github.com/lxc/incus/shared/api"
)

// InternalListener represents a internal event listener.
type InternalListener struct {
	handlers       map[string]EventHandler
	listener       *Listener
	server         *Server
	ctx            context.Context
	listenerCtx    context.Context
	listenerCancel context.CancelFunc
	lock           sync.Mutex
}

// NewInternalListener returns an InternalListener.
func NewInternalListener(ctx context.Context, server *Server) *InternalListener {
	return &InternalListener{
		ctx:      ctx,
		handlers: map[string]EventHandler{},
		server:   server,
	}
}

// startListener creates a new listener connection and listener. Also, it starts the gorountines
// needed to notify any registered handlers about new events.
func (l *InternalListener) startListener() {
	var err error

	l.listenerCtx, l.listenerCancel = context.WithCancel(l.ctx)
	aEnd, bEnd := memorypipe.NewPipePair(l.listenerCtx)
	listenerConnection := NewSimpleListenerConnection(aEnd)

	l.listener, err = l.server.AddListener("", true, nil, listenerConnection, []string{"lifecycle", "logging", "network-acl"}, []EventSource{EventSourcePull}, nil, nil)
	if err != nil {
		return
	}

	go func(ctx context.Context) {
		l.listener.Wait(ctx)
		l.listener.Close()
		l.listener = nil
	}(l.listenerCtx)

	go func(ctx context.Context, handlers map[string]EventHandler) {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var event api.Event

				_ = json.NewDecoder(bEnd).Decode(&event)

				for _, handler := range handlers {
					if handler == nil {
						continue
					}

					go handler(event)
				}
			}
		}
	}(l.listenerCtx, l.handlers)
}

// stopListener cancels the context thus stopping the listener.
func (l *InternalListener) stopListener() {
	if l.listenerCancel != nil {
		l.listenerCancel()
	}
}

// AddHandler adds a new event handler.
func (l *InternalListener) AddHandler(name string, handler EventHandler) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if handler == nil {
		return
	}

	// Add handler to the list of handlers.
	l.handlers[name] = handler

	if l.listener == nil {
		// Create a listener if necessary. This avoids having a listener around if there are no handlers.
		l.startListener()
	}
}

// RemoveHandler removes the event handler with the given name.
func (l *InternalListener) RemoveHandler(name string) {
	l.lock.Lock()
	defer l.lock.Unlock()

	for handlerName := range l.handlers {
		if handlerName == name {
			delete(l.handlers, name)
			break
		}
	}

	if len(l.handlers) == 0 {
		// Stop listener to avoid unnecessary goroutines.
		l.stopListener()
	}
}
