package events

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cancel"
)

// DevIncusServer represents an instance of an devIncus event server.
type DevIncusServer struct {
	serverCommon

	listeners map[string]*DevIncusListener
}

// NewDevIncusServer returns a new devIncus event server.
func NewDevIncusServer(debug bool, verbose bool) *DevIncusServer {
	server := &DevIncusServer{
		serverCommon: serverCommon{
			debug:   debug,
			verbose: verbose,
		},
		listeners: map[string]*DevIncusListener{},
	}

	return server
}

// AddListener creates and returns a new event listener.
func (s *DevIncusServer) AddListener(instanceID int, connection EventListenerConnection, messageTypes []string) (*DevIncusListener, error) {
	listener := &DevIncusListener{
		listenerCommon: listenerCommon{
			EventListenerConnection: connection,
			messageTypes:            messageTypes,
			done:                    cancel.New(context.Background()),
			id:                      uuid.New().String(),
			writeQueue:              make(chan api.Event, eventQueueSize),
		},
		instanceID: instanceID,
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.listeners[listener.id] != nil {
		return nil, fmt.Errorf("A listener with ID %q already exists", listener.id)
	}

	s.listeners[listener.id] = listener

	go listener.start()

	return listener, nil
}

// Send broadcasts a custom event.
func (s *DevIncusServer) Send(instanceID int, eventType string, eventMessage any) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}

	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
	}

	return s.broadcast(instanceID, event)
}

func (s *DevIncusServer) broadcast(instanceID int, event api.Event) error {
	s.lock.Lock()
	listeners := s.listeners
	for _, listener := range listeners {
		// Drop listeners that are already gone.
		if listener.IsClosed() {
			delete(s.listeners, listener.id)
			continue
		}

		if !slices.Contains(listener.messageTypes, event.Type) {
			continue
		}

		if listener.instanceID != instanceID {
			continue
		}

		// Queue the event so a slow listener doesn't hold up the others or lose ordering.
		err := listener.enqueue(event)
		if err != nil {
			delete(s.listeners, listener.id)
			listener.Close()
		}
	}

	s.lock.Unlock()

	return nil
}

// DevIncusListener describes a devIncus event listener.
type DevIncusListener struct {
	listenerCommon

	instanceID int
}
