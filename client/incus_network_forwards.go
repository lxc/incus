package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkForwardAddresses returns a list of network forward listen addresses.
func (r *ProtocolIncus) GetNetworkForwardAddresses(networkName string) ([]string, error) {
	if !r.HasExtension("network_forward") {
		return nil, errors.New(`The server is missing the required "network_forward" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/networks/%s/forwards", url.PathEscape(networkName))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkForwards returns a list of Network forward structs.
func (r *ProtocolIncus) GetNetworkForwards(networkName string) ([]api.NetworkForward, error) {
	if !r.HasExtension("network_forward") {
		return nil, errors.New(`The server is missing the required "network_forward" API extension`)
	}

	forwards := []api.NetworkForward{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s/forwards?recursion=1", url.PathEscape(networkName)), nil, "", &forwards)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetNetworkForward returns a Network forward entry for the provided network and listen address.
func (r *ProtocolIncus) GetNetworkForward(networkName string, listenAddress string) (*api.NetworkForward, string, error) {
	if !r.HasExtension("network_forward") {
		return nil, "", errors.New(`The server is missing the required "network_forward" API extension`)
	}

	forward := api.NetworkForward{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s/forwards/%s", url.PathEscape(networkName), url.PathEscape(listenAddress)), nil, "", &forward)
	if err != nil {
		return nil, "", err
	}

	return &forward, etag, nil
}

// CreateNetworkForward defines a new network forward using the provided struct.
func (r *ProtocolIncus) CreateNetworkForward(networkName string, forward api.NetworkForwardsPost) error {
	if !r.HasExtension("network_forward") {
		return errors.New(`The server is missing the required "network_forward" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", fmt.Sprintf("/networks/%s/forwards", url.PathEscape(networkName)), forward, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkForward updates the network forward to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkForward(networkName string, listenAddress string, forward api.NetworkForwardPut, ETag string) error {
	if !r.HasExtension("network_forward") {
		return errors.New(`The server is missing the required "network_forward" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/networks/%s/forwards/%s", url.PathEscape(networkName), url.PathEscape(listenAddress)), forward, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkForward deletes an existing network forward.
func (r *ProtocolIncus) DeleteNetworkForward(networkName string, listenAddress string) error {
	if !r.HasExtension("network_forward") {
		return errors.New(`The server is missing the required "network_forward" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/networks/%s/forwards/%s", url.PathEscape(networkName), url.PathEscape(listenAddress)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
