package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkIntegrationNames returns a list of network integration names.
func (r *ProtocolIncus) GetNetworkIntegrationNames() ([]string, error) {
	if !r.HasExtension("network_integrations") {
		return nil, errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-integrations"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkIntegrations returns a list of network integration structs.
func (r *ProtocolIncus) GetNetworkIntegrations() ([]api.NetworkIntegration, error) {
	if !r.HasExtension("network_integrations") {
		return nil, errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	integrations := []api.NetworkIntegration{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-integrations?recursion=1", nil, "", &integrations)
	if err != nil {
		return nil, err
	}

	return integrations, nil
}

// GetNetworkIntegration returns a network integration entry.
func (r *ProtocolIncus) GetNetworkIntegration(name string) (*api.NetworkIntegration, string, error) {
	if !r.HasExtension("network_integrations") {
		return nil, "", errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	integration := api.NetworkIntegration{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-integrations/%s", url.PathEscape(name)), nil, "", &integration)
	if err != nil {
		return nil, "", err
	}

	return &integration, etag, nil
}

// CreateNetworkIntegration defines a new network integration using the provided struct.
// Returns true if the integration connection has been mutually created. Returns false if integrationing has been only initiated.
func (r *ProtocolIncus) CreateNetworkIntegration(integration api.NetworkIntegrationsPost) error {
	if !r.HasExtension("network_integrations") {
		return errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-integrations", integration, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkIntegration updates the network integration to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkIntegration(name string, integration api.NetworkIntegrationPut, ETag string) error {
	if !r.HasExtension("network_integrations") {
		return errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-integrations/%s", url.PathEscape(name)), integration, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkIntegration renames an existing network integration entry.
func (r *ProtocolIncus) RenameNetworkIntegration(name string, network api.NetworkIntegrationPost) error {
	if !r.HasExtension("network_integrations") {
		return errors.New("The server is missing the required \"network_integrations\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/network-integrations/%s", url.PathEscape(name)), network, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkIntegration deletes an existing network integration.
func (r *ProtocolIncus) DeleteNetworkIntegration(name string) error {
	if !r.HasExtension("network_integrations") {
		return errors.New(`The server is missing the required "network_integrations" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-integrations/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
