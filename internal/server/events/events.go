package events

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/cancel"
	"github.com/lxc/incus/shared/logger"
)

// EventSource indicates the source of an event.
type EventSource uint8

// EventSourceLocal indicates the event was generated locally.
const EventSourceLocal = 0

// EventSourcePull indicates the event was received from an outbound event listener stream.
const EventSourcePull = 1

// EventSourcePush indicates the event was received from an event listener client connected to us.
const EventSourcePush = 2

// InjectFunc is used to inject an event received by a listener into the local events dispatcher.
type InjectFunc func(event api.Event, eventSource EventSource)

// NotifyFunc is called when an event is dispatched.
type NotifyFunc func(event api.Event)

// Server represents an instance of an event server.
type Server struct {
	serverCommon

	listeners map[string]*Listener
	notify    NotifyFunc
	location  string
}

// NewServer returns a new event server.
func NewServer(debug bool, verbose bool, notify NotifyFunc) *Server {
	server := &Server{
		serverCommon: serverCommon{
			debug:   debug,
			verbose: verbose,
		},
		listeners: map[string]*Listener{},
		notify:    notify,
	}

	return server
}

// SetLocalLocation sets the local location of this member.
// This value will be added to the Location event field if not populated from another member.
func (s *Server) SetLocalLocation(location string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.location = location
}

// AddListener creates and returns a new event listener.
func (s *Server) AddListener(projectName string, allProjects bool, projectPermissionFunc auth.PermissionChecker, connection EventListenerConnection, messageTypes []string, excludeSources []EventSource, recvFunc EventHandler, excludeLocations []string) (*Listener, error) {
	if allProjects && projectName != "" {
		return nil, fmt.Errorf("Cannot specify project name when listening for events on all projects")
	}

	if projectPermissionFunc == nil {
		projectPermissionFunc = func(auth.Object) bool {
			return true
		}
	}

	listener := &Listener{
		listenerCommon: listenerCommon{
			EventListenerConnection: connection,
			messageTypes:            messageTypes,
			done:                    cancel.New(context.Background()),
			id:                      uuid.New().String(),
			recvFunc:                recvFunc,
		},

		allProjects:           allProjects,
		projectName:           projectName,
		projectPermissionFunc: projectPermissionFunc,
		excludeSources:        excludeSources,
		excludeLocations:      excludeLocations,
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

// SendLifecycle broadcasts a lifecycle event.
func (s *Server) SendLifecycle(projectName string, event api.EventLifecycle) {
	_ = s.Send(projectName, api.EventTypeLifecycle, event)
}

// Send broadcasts a custom event.
func (s *Server) Send(projectName string, eventType string, eventMessage any) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}

	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
		Project:   projectName,
	}

	return s.broadcast(event, EventSourceLocal)
}

// Inject an event from another member into the local events dispatcher.
// eventSource is used to indicate where this event was received from.
func (s *Server) Inject(event api.Event, eventSource EventSource) {
	if event.Type == api.EventTypeLogging {
		// Parse the message
		logEntry := api.EventLogging{}
		err := json.Unmarshal(event.Metadata, &logEntry)
		if err != nil {
			return
		}

		if !s.debug && logEntry.Level == "debug" {
			return
		}

		if !s.debug && !s.verbose && logEntry.Level == "info" {
			return
		}
	}

	err := s.broadcast(event, eventSource)
	if err != nil {
		logger.Warn("Failed to forward event from member", logger.Ctx{"member": event.Location, "err": err})
	}
}

func (s *Server) broadcast(event api.Event, eventSource EventSource) error {
	sourceInSlice := func(source EventSource, sources []EventSource) bool {
		for _, i := range sources {
			if source == i {
				return true
			}
		}

		return false
	}

	s.lock.Lock()

	// Set the Location for local events to the local serverName if not already populated (do it here rather
	// than in Send as the lock to read s.location has been taken here already).
	if eventSource == EventSourceLocal && event.Location == "" {
		event.Location = s.location
	}

	// If a notifcation hook is present, then call it for locally produced events.
	// This can be used to send local events to another target (such as an event-hub member).
	if s.notify != nil && eventSource == EventSourceLocal {
		s.notify(event)
	}

	listeners := s.listeners
	for _, listener := range listeners {
		// If the event is project specific, check if the listener is requesting events from that project.
		if event.Project != "" && !listener.allProjects && event.Project != listener.projectName {
			continue
		}

		// If the event is project specific, ensure we have permission to view it.
		if event.Project != "" && !listener.projectPermissionFunc(auth.ObjectProject(event.Project)) {
			continue
		}

		if sourceInSlice(eventSource, listener.excludeSources) {
			continue
		}

		if !slices.Contains(listener.messageTypes, event.Type) {
			continue
		}

		// If the event doesn't come from this member and has been excluded by listener, don't deliver it.
		if eventSource != EventSourceLocal && slices.Contains(listener.excludeLocations, event.Location) {
			continue
		}

		go func(listener *Listener, event api.Event) {
			// Check that the listener still exists
			if listener == nil {
				return
			}

			// Make sure we're not done already
			if listener.IsClosed() {
				// Remove the listener from the list
				s.lock.Lock()
				delete(s.listeners, listener.id)
				s.lock.Unlock()
				return
			}

			err := listener.WriteJSON(event)
			if err != nil {
				// Remove the listener from the list
				s.lock.Lock()
				delete(s.listeners, listener.id)
				s.lock.Unlock()

				listener.Close()
			}
		}(listener, event)
	}

	s.lock.Unlock()

	return nil
}

// Listener describes an event listener.
type Listener struct {
	listenerCommon

	allProjects           bool
	projectName           string
	projectPermissionFunc auth.PermissionChecker
	excludeSources        []EventSource
	excludeLocations      []string
}
