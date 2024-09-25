package api

import (
	"encoding/json"
)

// ResponseRaw represents an operation in its original form.
type ResponseRaw struct {
	Type ResponseType `json:"type" yaml:"type"`

	// Valid only for Sync responses
	Status     string `json:"status" yaml:"status"`
	StatusCode int    `json:"status_code" yaml:"status_code"`

	// Valid only for Async responses
	Operation string `json:"operation" yaml:"operation"`

	// Valid only for Error responses
	Code  int    `json:"error_code" yaml:"error_code"`
	Error string `json:"error" yaml:"error"`

	Metadata any `json:"metadata" yaml:"metadata"`
}

// Response represents an operation.
type Response struct {
	Type ResponseType `json:"type" yaml:"type"`

	// Valid only for Sync responses
	Status     string `json:"status" yaml:"status"`
	StatusCode int    `json:"status_code" yaml:"status_code"`

	// Valid only for Async responses
	Operation string `json:"operation" yaml:"operation"`

	// Valid only for Error responses
	Code  int    `json:"error_code" yaml:"error_code"`
	Error string `json:"error" yaml:"error"`

	// Valid for Sync and Error responses
	Metadata json.RawMessage `json:"metadata" yaml:"metadata"`
}

// MetadataAsMap parses the Response metadata into a map.
func (r *Response) MetadataAsMap() (map[string]any, error) {
	ret := map[string]any{}
	err := r.MetadataAsStruct(&ret)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// MetadataAsOperation turns the Response metadata into an Operation.
func (r *Response) MetadataAsOperation() (*Operation, error) {
	op := Operation{}
	err := r.MetadataAsStruct(&op)
	if err != nil {
		return nil, err
	}

	return &op, nil
}

// MetadataAsStringSlice parses the Response metadata into a slice of string.
func (r *Response) MetadataAsStringSlice() ([]string, error) {
	sl := []string{}
	err := r.MetadataAsStruct(&sl)
	if err != nil {
		return nil, err
	}

	return sl, nil
}

// MetadataAsStruct parses the Response metadata into a provided struct.
func (r *Response) MetadataAsStruct(target any) error {
	return json.Unmarshal(r.Metadata, &target)
}

// ResponseType represents a valid response type.
type ResponseType string

// Response types.
const (
	SyncResponse  ResponseType = "sync"
	AsyncResponse ResponseType = "async"
	ErrorResponse ResponseType = "error"
)
