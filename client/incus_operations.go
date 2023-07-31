package incus

import (
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
)

// GetOperationUUIDs returns a list of operation uuids.
func (r *ProtocolLXD) GetOperationUUIDs() ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/operations"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetOperations returns a list of Operation struct.
func (r *ProtocolLXD) GetOperations() ([]api.Operation, error) {
	apiOperations := map[string][]api.Operation{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/operations?recursion=1", nil, "", &apiOperations)
	if err != nil {
		return nil, err
	}

	// Turn it into just a list of operations
	operations := []api.Operation{}
	for _, v := range apiOperations {
		operations = append(operations, v...)
	}

	return operations, nil
}

// GetOperation returns an Operation entry for the provided uuid.
func (r *ProtocolLXD) GetOperation(uuid string) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s", url.PathEscape(uuid)), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWait returns an Operation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolLXD) GetOperationWait(uuid string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s/wait?timeout=%d", url.PathEscape(uuid), timeout), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWaitSecret returns an Operation entry for the provided uuid and secret once it's complete or hits the timeout.
func (r *ProtocolLXD) GetOperationWaitSecret(uuid string, secret string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s/wait?secret=%s&timeout=%d", url.PathEscape(uuid), url.PathEscape(secret), timeout), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWebsocket returns a websocket connection for the provided operation.
func (r *ProtocolLXD) GetOperationWebsocket(uuid string, secret string) (*websocket.Conn, error) {
	path := fmt.Sprintf("/operations/%s/websocket", url.PathEscape(uuid))
	if secret != "" {
		path = fmt.Sprintf("%s?secret=%s", path, url.QueryEscape(secret))
	}

	return r.websocket(path)
}

// DeleteOperation deletes (cancels) a running operation.
func (r *ProtocolLXD) DeleteOperation(uuid string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/operations/%s", url.PathEscape(uuid)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
