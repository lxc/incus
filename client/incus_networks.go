package incus

import (
	"fmt"
	"net/url"

	"github.com/lxc/incus/shared/api"
)

// GetNetworkNames returns a list of network names.
func (r *ProtocolIncus) GetNetworkNames() ([]string, error) {
	if !r.HasExtension("network") {
		return nil, fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	// Fetch the raw values.
	urls := []string{}
	baseURL := "/networks"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworks returns a list of Network struct.
func (r *ProtocolIncus) GetNetworks() ([]api.Network, error) {
	if !r.HasExtension("network") {
		return nil, fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	networks := []api.Network{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/networks?recursion=1", nil, "", &networks)
	if err != nil {
		return nil, err
	}

	return networks, nil
}

// GetNetwork returns a Network entry for the provided name.
func (r *ProtocolIncus) GetNetwork(name string) (*api.Network, string, error) {
	if !r.HasExtension("network") {
		return nil, "", fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	network := api.Network{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s", url.PathEscape(name)), nil, "", &network)
	if err != nil {
		return nil, "", err
	}

	return &network, etag, nil
}

// GetNetworkLeases returns a list of Network struct.
func (r *ProtocolIncus) GetNetworkLeases(name string) ([]api.NetworkLease, error) {
	if !r.HasExtension("network_leases") {
		return nil, fmt.Errorf("The server is missing the required \"network_leases\" API extension")
	}

	leases := []api.NetworkLease{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s/leases", url.PathEscape(name)), nil, "", &leases)
	if err != nil {
		return nil, err
	}

	return leases, nil
}

// GetNetworkState returns metrics and information on the running network.
func (r *ProtocolIncus) GetNetworkState(name string) (*api.NetworkState, error) {
	if !r.HasExtension("network_state") {
		return nil, fmt.Errorf("The server is missing the required \"network_state\" API extension")
	}

	state := api.NetworkState{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s/state", url.PathEscape(name)), nil, "", &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// CreateNetwork defines a new network using the provided Network struct.
func (r *ProtocolIncus) CreateNetwork(network api.NetworksPost) error {
	if !r.HasExtension("network") {
		return fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", "/networks", network, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetwork updates the network to match the provided Network struct.
func (r *ProtocolIncus) UpdateNetwork(name string, network api.NetworkPut, ETag string) error {
	if !r.HasExtension("network") {
		return fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/networks/%s", url.PathEscape(name)), network, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetwork renames an existing network entry.
func (r *ProtocolIncus) RenameNetwork(name string, network api.NetworkPost) error {
	if !r.HasExtension("network") {
		return fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/networks/%s", url.PathEscape(name)), network, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetwork deletes an existing network.
func (r *ProtocolIncus) DeleteNetwork(name string) error {
	if !r.HasExtension("network") {
		return fmt.Errorf("The server is missing the required \"network\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/networks/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
