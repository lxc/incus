package incus

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkACLNames returns a list of network ACL names.
func (r *ProtocolIncus) GetNetworkACLNames() ([]string, error) {
	if !r.HasExtension("network_acl") {
		return nil, errors.New(`The server is missing the required "network_acl" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-acls"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkACLs returns a list of Network ACL structs.
func (r *ProtocolIncus) GetNetworkACLs() ([]api.NetworkACL, error) {
	if !r.HasExtension("network_acl") {
		return nil, errors.New(`The server is missing the required "network_acl" API extension`)
	}

	acls := []api.NetworkACL{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-acls?recursion=1", nil, "", &acls)
	if err != nil {
		return nil, err
	}

	return acls, nil
}

// GetNetworkACLsAllProjects returns all list of Network ACL structs across all projects.
func (r *ProtocolIncus) GetNetworkACLsAllProjects() ([]api.NetworkACL, error) {
	if !r.HasExtension("network_acls_all_projects") {
		return nil, errors.New(`The server is missing the required "network_acls_all_projects" API extension`)
	}

	acls := []api.NetworkACL{}
	_, err := r.queryStruct("GET", "/network-acls?recursion=1&all-projects=true", nil, "", &acls)
	if err != nil {
		return nil, err
	}

	return acls, nil
}

// GetNetworkACL returns a Network ACL entry for the provided name.
func (r *ProtocolIncus) GetNetworkACL(name string) (*api.NetworkACL, string, error) {
	if !r.HasExtension("network_acl") {
		return nil, "", errors.New(`The server is missing the required "network_acl" API extension`)
	}

	acl := api.NetworkACL{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), nil, "", &acl)
	if err != nil {
		return nil, "", err
	}

	return &acl, etag, nil
}

// GetNetworkACLLogfile returns a reader for the ACL log file.
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolIncus) GetNetworkACLLogfile(name string) (io.ReadCloser, error) {
	if !r.HasExtension("network_acl_log") {
		return nil, errors.New(`The server is missing the required "network_acl_log" API extension`)
	}

	// Prepare the HTTP request
	uri := fmt.Sprintf("%s/1.0/network-acls/%s/log", r.httpBaseURL.String(), url.PathEscape(name))
	uri, err := r.setQueryAttributes(uri)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// CreateNetworkACL defines a new network ACL using the provided struct.
func (r *ProtocolIncus) CreateNetworkACL(acl api.NetworkACLsPost) error {
	if !r.HasExtension("network_acl") {
		return errors.New(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-acls", acl, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkACL updates the network ACL to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkACL(name string, acl api.NetworkACLPut, ETag string) error {
	if !r.HasExtension("network_acl") {
		return errors.New(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), acl, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkACL renames an existing network ACL entry.
func (r *ProtocolIncus) RenameNetworkACL(name string, acl api.NetworkACLPost) error {
	if !r.HasExtension("network_acl") {
		return errors.New(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), acl, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkACL deletes an existing network ACL.
func (r *ProtocolIncus) DeleteNetworkACL(name string) error {
	if !r.HasExtension("network_acl") {
		return errors.New(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
