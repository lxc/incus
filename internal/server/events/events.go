package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cancel"
	"github.com/lxc/incus/v7/shared/logger"
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
		return nil, errors.New("Cannot specify project name when listening for events on all projects")
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
			writeQueue:              make(chan api.Event, eventQueueSize),
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
	}

	err := s.broadcast(event, eventSource)
	if err != nil {
		logger.Warn("Failed to forward event from member", logger.Ctx{"member": event.Location, "err": err})
	}
}

func (s *Server) broadcast(event api.Event, eventSource EventSource) error {
	sourceInSlice := func(source EventSource, sources []EventSource) bool {
		return slices.Contains(sources, source)
	}

	s.lock.Lock()

	// Set the Location for local events to the local serverName if not already populated (do it here rather
	// than in Send as the lock to read s.location has been taken here already).
	if eventSource == EventSourceLocal && event.Location == "" {
		event.Location = s.location
	}

	notify := s.notify
	listeners := make([]*Listener, 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}

	s.lock.Unlock()

	// Dispatch outside of the lock as both the notification hook and the listener filters can block,
	// which would otherwise also block anything logging (logging is fed back through broadcast).
	if notify != nil && eventSource == EventSourceLocal {
		notify(event)
	}

	removeListener := func(listener *Listener) {
		s.lock.Lock()
		delete(s.listeners, listener.id)
		s.lock.Unlock()
	}

	for _, listener := range listeners {
		// Drop listeners that are already gone.
		if listener.IsClosed() {
			removeListener(listener)
			continue
		}

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

		// Queue the event so a slow listener doesn't hold up the others or lose ordering.
		err := listener.enqueue(event)
		if err != nil {
			removeListener(listener)
			listener.Close()
		}
	}

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
