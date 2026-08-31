package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v7/shared/api"
)

// GetNetworkPeerGroupNames returns a list of network peer group names.
func (r *ProtocolIncus) GetNetworkPeerGroupNames() ([]string, error) {
	if !r.HasExtension("network_peer_groups") {
		return nil, errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-peer-groups"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkPeerGroups returns a list of network peer group structs.
func (r *ProtocolIncus) GetNetworkPeerGroups() ([]api.NetworkPeerGroup, error) {
	if !r.HasExtension("network_peer_groups") {
		return nil, errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	groups := []api.NetworkPeerGroup{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-peer-groups?recursion=1", nil, "", &groups)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

// GetNetworkPeerGroup returns a network peer group entry.
func (r *ProtocolIncus) GetNetworkPeerGroup(name string) (*api.NetworkPeerGroup, string, error) {
	if !r.HasExtension("network_peer_groups") {
		return nil, "", errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	group := api.NetworkPeerGroup{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-peer-groups/%s", url.PathEscape(name)), nil, "", &group)
	if err != nil {
		return nil, "", err
	}

	return &group, etag, nil
}

// CreateNetworkPeerGroup defines a new network peer group using the provided struct.
func (r *ProtocolIncus) CreateNetworkPeerGroup(group api.NetworkPeerGroupsPost) error {
	if !r.HasExtension("network_peer_groups") {
		return errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-peer-groups", group, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkPeerGroup updates the network peer group to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkPeerGroup(name string, group api.NetworkPeerGroupPut, ETag string) error {
	if !r.HasExtension("network_peer_groups") {
		return errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-peer-groups/%s", url.PathEscape(name)), group, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkPeerGroup deletes an existing network peer group.
func (r *ProtocolIncus) DeleteNetworkPeerGroup(name string) error {
	if !r.HasExtension("network_peer_groups") {
		return errors.New(`The server is missing the required "network_peer_groups" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-peer-groups/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
