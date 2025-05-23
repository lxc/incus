package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkAddressSetNames returns a list of network address set names.
func (r *ProtocolIncus) GetNetworkAddressSetNames() ([]string, error) {
	if !r.HasExtension("network_address_set") {
		return nil, errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-address-sets"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkAddressSets returns a list of network address set structs.
func (r *ProtocolIncus) GetNetworkAddressSets() ([]api.NetworkAddressSet, error) {
	if !r.HasExtension("network_address_set") {
		return nil, errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	addressSets := []api.NetworkAddressSet{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-address-sets?recursion=1", nil, "", &addressSets)
	if err != nil {
		return nil, err
	}

	return addressSets, nil
}

// GetNetworkAddressSetsAllProjects returns a list of network address set structs across all projects.
func (r *ProtocolIncus) GetNetworkAddressSetsAllProjects() ([]api.NetworkAddressSet, error) {
	if !r.HasExtension("network_address_set") {
		return nil, errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	addressSets := []api.NetworkAddressSet{}
	_, err := r.queryStruct("GET", "/network-address-sets?recursion=1&all-projects=true", nil, "", &addressSets)
	if err != nil {
		return nil, err
	}

	return addressSets, nil
}

// GetNetworkAddressSet returns a network address set entry for the provided name.
func (r *ProtocolIncus) GetNetworkAddressSet(name string) (*api.NetworkAddressSet, string, error) {
	if !r.HasExtension("network_address_set") {
		return nil, "", errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	addrSet := api.NetworkAddressSet{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-address-sets/%s", url.PathEscape(name)), nil, "", &addrSet)
	if err != nil {
		return nil, "", err
	}

	return &addrSet, etag, nil
}

// CreateNetworkAddressSet defines a new network address set using the provided struct.
func (r *ProtocolIncus) CreateNetworkAddressSet(as api.NetworkAddressSetsPost) error {
	if !r.HasExtension("network_address_set") {
		return errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-address-sets", as, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkAddressSet updates the network address set to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkAddressSet(name string, as api.NetworkAddressSetPut, ETag string) error {
	if !r.HasExtension("network_address_set") {
		return errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-address-sets/%s", url.PathEscape(name)), as, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkAddressSet renames an existing network address set entry.
func (r *ProtocolIncus) RenameNetworkAddressSet(name string, as api.NetworkAddressSetPost) error {
	if !r.HasExtension("network_address_set") {
		return errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", fmt.Sprintf("/network-address-sets/%s", url.PathEscape(name)), as, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkAddressSet deletes an existing network address set.
func (r *ProtocolIncus) DeleteNetworkAddressSet(name string) error {
	if !r.HasExtension("network_address_set") {
		return errors.New(`The server is missing the required "network_address_set" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-address-sets/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
