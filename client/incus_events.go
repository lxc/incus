package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
)

// Event handling functions

// getEvents connects to the Incus monitoring interface.
func (r *ProtocolIncus) getEvents(allProjects bool) (*EventListener, error) {
	// Prevent anything else from interacting with the listeners
	r.eventListenersLock.Lock()
	defer r.eventListenersLock.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	// Setup a new listener
	listener := EventListener{
		r:         r,
		ctx:       ctx,
		ctxCancel: cancel,
	}

	connInfo, _ := r.GetConnectionInfo()
	if connInfo.Project == "" {
		return nil, fmt.Errorf("Unexpected empty project in connection info")
	}

	if !allProjects {
		listener.projectName = connInfo.Project
	}

	// There is an existing Go routine for the required project filter, so just add another target.
	if r.eventListeners[listener.projectName] != nil {
		r.eventListeners[listener.projectName] = append(r.eventListeners[listener.projectName], &listener)
		return &listener, nil
	}

	// Setup a new connection with Incus
	var url string
	var err error
	if allProjects {
		url, err = r.setQueryAttributes("/events?all-projects=true")
	} else {
		url, err = r.setQueryAttributes("/events")
	}

	if err != nil {
		return nil, err
	}

	// Connect websocket and save.
	wsConn, err := r.websocket(url)
	if err != nil {
		return nil, err
	}

	r.eventConnsLock.Lock()
	r.eventConns[listener.projectName] = wsConn // Save for others to use.
	r.eventConnsLock.Unlock()

	// Initialize the event listener list if we were able to connect to the events websocket.
	r.eventListeners[listener.projectName] = []*EventListener{&listener}

	// Spawn a watcher that will close the websocket connection after all
	// listeners are gone.
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-time.After(time.Minute):
			case <-r.ctxConnected.Done():
			case <-stopCh:
			}

			r.eventListenersLock.Lock()
			r.eventConnsLock.Lock()
			if len(r.eventListeners[listener.projectName]) == 0 {
				// We don't need the connection anymore, disconnect and clear.
				if r.eventListeners[listener.projectName] != nil {
					_ = r.eventConns[listener.projectName].Close()
					delete(r.eventConns, listener.projectName)
				}

				r.eventListeners[listener.projectName] = nil
				r.eventListenersLock.Unlock()
				r.eventConnsLock.Unlock()

				return
			}

			r.eventListenersLock.Unlock()
			r.eventConnsLock.Unlock()
		}
	}()

	// Spawn the listener
	go func() {
		for {
			_, data, err := wsConn.ReadMessage()
			if err != nil {
				// Prevent anything else from interacting with the listeners
				r.eventListenersLock.Lock()
				defer r.eventListenersLock.Unlock()

				// Tell all the current listeners about the failure
				for _, listener := range r.eventListeners[listener.projectName] {
					listener.err = err
					listener.ctxCancel()
				}

				// And remove them all from the list so that when watcher routine runs it will
				// close the websocket connection.
				r.eventListeners[listener.projectName] = nil

				close(stopCh) // Instruct watcher go routine to cleanup.

				return
			}

			// Attempt to unpack the message
			event := api.Event{}
			err = json.Unmarshal(data, &event)
			if err != nil {
				continue
			}

			// Extract the message type
			if event.Type == "" {
				continue
			}

			// Send the message to all handlers
			r.eventListenersLock.Lock()
			for _, listener := range r.eventListeners[listener.projectName] {
				listener.targetsLock.Lock()
				for _, target := range listener.targets {
					if target.types != nil && !slices.Contains(target.types, event.Type) {
						continue
					}

					go target.function(event)
				}

				listener.targetsLock.Unlock()
			}

			r.eventListenersLock.Unlock()
		}
	}()

	return &listener, nil
}

// GetEvents gets the events for the project defined on the client.
func (r *ProtocolIncus) GetEvents() (*EventListener, error) {
	return r.getEvents(false)
}

// GetEventsAllProjects gets events for all projects.
func (r *ProtocolIncus) GetEventsAllProjects() (*EventListener, error) {
	return r.getEvents(true)
}

// SendEvent send an event to the server via the client's event listener connection.
func (r *ProtocolIncus) SendEvent(event api.Event) error {
	r.eventConnsLock.Lock()
	defer r.eventConnsLock.Unlock()

	// Find an available event listener connection.
	// It doesn't matter which project the event listener connection is using, as this only affects which
	// events are received from the server, not which events we can send to it.
	var eventConn *websocket.Conn
	for _, eventConn = range r.eventConns {
		break
	}

	if eventConn == nil {
		return fmt.Errorf("No available event listener connection")
	}

	deadline, ok := r.ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}

	_ = eventConn.SetWriteDeadline(deadline)
	return eventConn.WriteJSON(event)
}
