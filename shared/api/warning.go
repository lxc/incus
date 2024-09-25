package api

import (
	"time"
)

// Warning represents a warning entry.
//
// swagger:model
//
// API extension: warnings.
type Warning struct {
	WarningPut `yaml:",inline"`

	// UUID of the warning
	// Example: e9e9da0d-2538-4351-8047-46d4a8ae4dbb
	UUID string `json:"uuid" yaml:"uuid"`

	// What cluster member this warning occurred on
	// Example: server01
	Location string `json:"location" yaml:"location"`

	// The project the warning occurred in
	// Example: default
	Project string `json:"project" yaml:"project"`

	// Type type of warning
	// Example: Couldn't find CGroup
	Type string `json:"type" yaml:"type"`

	// The number of times this warning occurred
	// Example: 1
	Count int `json:"count" yaml:"count"`

	// The first time this warning occurred
	// Example: 2021-03-23T17:38:37.753398689-04:00
	FirstSeenAt time.Time `json:"first_seen_at" yaml:"first_seen_at"`

	// The last time this warning occurred
	// Example: 2021-03-23T17:38:37.753398689-04:00
	LastSeenAt time.Time `json:"last_seen_at" yaml:"last_seen_at"`

	// The warning message
	// Example: Couldn't find the CGroup blkio.weight, disk priority will be ignored
	LastMessage string `json:"last_message" yaml:"last_message"`

	// The severity of this warning
	// Example: low
	Severity string `json:"severity" yaml:"severity"`

	// The entity affected by this warning
	// Example: /1.0/instances/c1?project=default
	EntityURL string `json:"entity_url" yaml:"entity_url"`
}

// WarningPut represents the modifiable fields of a warning.
//
// swagger:model
//
// API extension: warnings.
type WarningPut struct {
	// Status of the warning (new, acknowledged, or resolved)
	// Example: new
	Status string `json:"status" yaml:"status"`
}
