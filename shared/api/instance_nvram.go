package api

import (
	"encoding/base64"
	"time"
)

// InstanceNVRAMVariable represents a UEFI variable.
//
// swagger:model
//
// API extension: instance_nvram.
type InstanceNVRAMVariable struct {
	// Binary data.
	Binary []byte `json:"binary"`

	// Dissected data.
	Data any `json:"data,omitempty"`

	// Variable attributes.
	Attributes []string `json:"attributes"`

	// Authenticated variable timestamp.
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// MarshalYAML marshals binary variable data to base64.
func (v *InstanceNVRAMVariable) MarshalYAML() (any, error) {
	return struct {
		Binary     string     `yaml:"binary,omitempty"`
		Data       any        `yaml:"data,omitempty"`
		Attributes []string   `yaml:"attributes"`
		Timestamp  *time.Time `yaml:"timestamp,omitempty"`
	}{
		Binary:     base64.StdEncoding.EncodeToString(v.Binary),
		Data:       v.Data,
		Attributes: v.Attributes,
		Timestamp:  v.Timestamp,
	}, nil
}
