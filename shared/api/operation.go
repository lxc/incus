package api

import (
	"errors"
	"fmt"
	"time"
)

// OperationClassTask represents the Task OperationClass.
const OperationClassTask = "task"

// OperationClassWebsocket represents the Websocket OperationClass.
const OperationClassWebsocket = "websocket"

// OperationClassToken represents the Token OperationClass.
const OperationClassToken = "token"

// Operation represents a background operation
//
// swagger:model
type Operation struct {
	// UUID of the operation
	// Example: 6916c8a6-9b7d-4abd-90b3-aedfec7ec7da
	ID string `json:"id" yaml:"id"`

	// Type of operation (task, token or websocket)
	// Example: websocket
	Class string `json:"class" yaml:"class"`

	// Description of the operation
	// Example: Executing command
	Description string `json:"description" yaml:"description"`

	// Operation creation time
	// Example: 2021-03-23T17:38:37.753398689-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// Operation last change
	// Example: 2021-03-23T17:38:37.753398689-04:00
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`

	// Status name
	// Example: Running
	Status string `json:"status" yaml:"status"`

	// Status code
	// Example: 103
	StatusCode StatusCode `json:"status_code" yaml:"status_code"`

	// Affected resources
	// Example: {"instances": ["/1.0/instances/foo"]}
	Resources map[string][]string `json:"resources" yaml:"resources"`

	// Operation specific metadata
	// Example: {"command": ["bash"], "environment": {"HOME": "/root", "LANG": "C.UTF-8", "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM": "xterm", "USER": "root"}, "fds": {"0": "da3046cf02c0116febf4ef3fe4eaecdf308e720c05e5a9c730ce1a6f15417f66", "1": "05896879d8692607bd6e4a09475667da3b5f6714418ab0ee0e5720b4c57f754b"}, "interactive": true}
	Metadata map[string]any `json:"metadata" yaml:"metadata"`

	// Whether the operation can be canceled
	// Example: false
	MayCancel bool `json:"may_cancel" yaml:"may_cancel"`

	// Operation error message
	// Example: Some error message
	Err string `json:"err" yaml:"err"`

	// What cluster member this record was found on
	// Example: server01
	//
	// API extension: operation_location
	Location string `json:"location" yaml:"location"`
}

// ToCertificateAddToken creates a certificate add token from the operation metadata.
func (op *Operation) ToCertificateAddToken() (*CertificateAddToken, error) {
	req, ok := op.Metadata["request"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Operation request is type %T not map[string]any", op.Metadata["request"])
	}

	clientName, ok := req["name"].(string)
	if !ok {
		return nil, errors.New("Failed to get client name")
	}

	secret, ok := op.Metadata["secret"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation secret is type %T not string", op.Metadata["secret"])
	}

	fingerprint, ok := op.Metadata["fingerprint"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation fingerprint is type %T not string", op.Metadata["fingerprint"])
	}

	addresses, ok := op.Metadata["addresses"].([]any)
	if !ok {
		return nil, fmt.Errorf("Operation addresses is type %T not []any", op.Metadata["addresses"])
	}

	joinToken := CertificateAddToken{
		ClientName:  clientName,
		Secret:      secret,
		Fingerprint: fingerprint,
		Addresses:   make([]string, 0, len(addresses)),
	}

	expiresAtStr, ok := op.Metadata["expiresAt"].(string)
	if ok {
		expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
		if err != nil {
			return nil, err
		}

		joinToken.ExpiresAt = expiresAt
	}

	for i, address := range addresses {
		addressString, ok := address.(string)
		if !ok {
			return nil, fmt.Errorf("Operation address index %d is type %T not string", i, address)
		}

		joinToken.Addresses = append(joinToken.Addresses, addressString)
	}

	return &joinToken, nil
}

// ToClusterJoinToken creates a cluster join token from the operation metadata.
func (op *Operation) ToClusterJoinToken() (*ClusterMemberJoinToken, error) {
	serverName, ok := op.Metadata["serverName"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation serverName is type %T not string", op.Metadata["serverName"])
	}

	secret, ok := op.Metadata["secret"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation secret is type %T not string", op.Metadata["secret"])
	}

	fingerprint, ok := op.Metadata["fingerprint"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation fingerprint is type %T not string", op.Metadata["fingerprint"])
	}

	addresses, ok := op.Metadata["addresses"].([]any)
	if !ok {
		return nil, fmt.Errorf("Operation addresses is type %T not []any", op.Metadata["addresses"])
	}

	expiresAtStr, ok := op.Metadata["expiresAt"].(string)
	if !ok {
		return nil, fmt.Errorf("Operation expiresAt is type %T not string", op.Metadata["expiresAt"])
	}

	expiresAt, err := time.Parse(time.RFC3339Nano, expiresAtStr)
	if err != nil {
		return nil, err
	}

	joinToken := ClusterMemberJoinToken{
		ServerName:  serverName,
		Secret:      secret,
		Fingerprint: fingerprint,
		Addresses:   make([]string, 0, len(addresses)),
		ExpiresAt:   expiresAt,
	}

	for i, address := range addresses {
		addressString, ok := address.(string)
		if !ok {
			return nil, fmt.Errorf("Operation address index %d is type %T not string", i, address)
		}

		joinToken.Addresses = append(joinToken.Addresses, addressString)
	}

	return &joinToken, nil
}
