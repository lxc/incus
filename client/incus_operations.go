package incus

import (
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
)

// GetOperationUUIDs returns a list of operation uuids.
func (r *ProtocolIncus) GetOperationUUIDs() ([]string, error) {
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
func (r *ProtocolIncus) GetOperations() ([]api.Operation, error) {
	apiOperations := map[string][]api.Operation{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/operations?recursion=1", nil, "", &apiOperations)
	if err != nil {
		return nil, err
	}

	// Turn it into a list of operations.
	operations := []api.Operation{}
	for _, v := range apiOperations {
		operations = append(operations, v...)
	}

	return operations, nil
}

// GetOperationsAllProjects returns a list of operations from all projects.
func (r *ProtocolIncus) GetOperationsAllProjects() ([]api.Operation, error) {
	err := r.CheckExtension("operations_get_query_all_projects")
	if err != nil {
		return nil, err
	}

	apiOperations := map[string][]api.Operation{}

	path := "/operations"

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &apiOperations)
	if err != nil {
		return nil, err
	}

	// Turn it into a list of operations.
	operations := []api.Operation{}
	for _, v := range apiOperations {
		operations = append(operations, v...)
	}

	return operations, nil
}

// GetOperation returns an Operation entry for the provided uuid.
func (r *ProtocolIncus) GetOperation(uuid string) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s", url.PathEscape(uuid)), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWait returns an Operation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolIncus) GetOperationWait(uuid string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Unset the response header timeout so that the request does not time out.
	transport, err := r.getUnderlyingHTTPTransport()
	if err != nil {
		return nil, "", err
	}

	transport.ResponseHeaderTimeout = 0

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s/wait?timeout=%d", url.PathEscape(uuid), timeout), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWaitSecret returns an Operation entry for the provided uuid and secret once it's complete or hits the timeout.
func (r *ProtocolIncus) GetOperationWaitSecret(uuid string, secret string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s/wait?secret=%s&timeout=%d", url.PathEscape(uuid), url.PathEscape(secret), timeout), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWebsocket returns a websocket connection for the provided operation.
func (r *ProtocolIncus) GetOperationWebsocket(uuid string, secret string) (*websocket.Conn, error) {
	path := fmt.Sprintf("/operations/%s/websocket", url.PathEscape(uuid))
	if secret != "" {
		path = fmt.Sprintf("%s?secret=%s", path, url.QueryEscape(secret))
	}

	return r.websocket(path)
}

// DeleteOperation deletes (cancels) a running operation.
func (r *ProtocolIncus) DeleteOperation(uuid string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/operations/%s", url.PathEscape(uuid)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
