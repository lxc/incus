package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event types.
const (
	EventTypeLifecycle  = "lifecycle"
	EventTypeLogging    = "logging"
	EventTypeOperation  = "operation"
	EventTypeNetworkACL = "network-acl"
)

// Event represents an event entry (over websocket)
//
// swagger:model
type Event struct {
	// Event type (one of operation, logging or lifecycle)
	// Example: lifecycle
	Type string `yaml:"type" json:"type"`

	// Time at which the event was sent
	// Example: 2021-02-24T19:00:45.452649098-05:00
	Timestamp time.Time `yaml:"timestamp" json:"timestamp"`

	// JSON encoded metadata (see EventLogging, EventLifecycle or Operation)
	// Example: {"action": "instance-started", "source": "/1.0/instances/c1", "context": {}}
	Metadata json.RawMessage `yaml:"metadata" json:"metadata"`

	// Originating cluster member
	// Example: server01
	//
	// API extension: event_location
	Location string `yaml:"location,omitempty" json:"location,omitempty"`

	// Project the event belongs to.
	// Example: default
	//
	// API extension: event_project
	Project string `yaml:"project,omitempty" json:"project,omitempty"`
}

// ToLogging creates log record for the event.
func (event *Event) ToLogging() (EventLogRecord, error) {
	switch event.Type {
	case EventTypeLogging, EventTypeNetworkACL:
		e := &EventLogging{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		ctx := []any{}
		for k, v := range e.Context {
			ctx = append(ctx, k)
			ctx = append(ctx, v)
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  e.Level,
			Msg:  e.Message,
			Ctx:  ctx,
		}

		return record, nil

	case EventTypeLifecycle:
		e := &EventLifecycle{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		ctx := []any{}
		for k, v := range e.Context {
			ctx = append(ctx, k)
			ctx = append(ctx, v)
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  "info",
			Ctx:  ctx,
		}

		if e.Requestor != nil {
			requestor := fmt.Sprintf("%s/%s (%s)", e.Requestor.Protocol, e.Requestor.Username, e.Requestor.Address)
			record.Msg = fmt.Sprintf("Action: %s, Source: %s, Requestor: %s", e.Action, e.Source, requestor)
		} else {
			record.Msg = fmt.Sprintf("Action: %s, Source: %s", e.Action, e.Source)
		}

		return record, nil

	case EventTypeOperation:
		e := &Operation{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  "info",
			Msg:  fmt.Sprintf("ID: %s, Class: %s, Description: %s", e.ID, e.Class, e.Description),
			Ctx: []any{
				"CreatedAt", e.CreatedAt,
				"UpdatedAt", e.UpdatedAt,
				"Status", e.Status,
				"StatusCode", e.StatusCode,
				"Resources", e.Resources,
				"Metadata", e.Metadata,
				"MayCancel", e.MayCancel,
				"Err", e.Err,
				"Location", e.Location,
			},
		}

		return record, nil
	}

	return EventLogRecord{}, fmt.Errorf("Not supported event type: %s", event.Type)
}

// EventLogRecord represents single log record.
type EventLogRecord struct {
	Time time.Time
	Lvl  string
	Msg  string
	Ctx  []any
}

// EventLogging represents a logging type event entry (admin only).
type EventLogging struct {
	Message string            `yaml:"message" json:"message"`
	Level   string            `yaml:"level" json:"level"`
	Context map[string]string `yaml:"context" json:"context"`
}

// EventLifecycle represents a lifecycle type event entry
//
// API extension: event_lifecycle.
type EventLifecycle struct {
	Action  string         `yaml:"action" json:"action"`
	Source  string         `yaml:"source" json:"source"`
	Context map[string]any `yaml:"context,omitempty" json:"context,omitempty"`

	// API extension: event_lifecycle_requestor
	Requestor *EventLifecycleRequestor `yaml:"requestor,omitempty" json:"requestor,omitempty"`

	// API extension: event_lifecycle_name_and_project
	Name    string `yaml:"name,omitempty" json:"name,omitempty"`
	Project string `yaml:"project,omitempty" json:"project,omitempty"`
}

// EventLifecycleRequestor represents the initial requestor for an event
//
// API extension: event_lifecycle_requestor.
type EventLifecycleRequestor struct {
	Username string `yaml:"username" json:"username"`
	Protocol string `yaml:"protocol" json:"protocol"`

	// Requestor address
	// Example: 10.0.2.15
	//
	// API extension: event_lifecycle_requestor_address
	Address string `yaml:"address" json:"address"`
}
