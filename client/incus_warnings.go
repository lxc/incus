package incus

import (
	"fmt"
	"net/url"

	"github.com/lxc/incus/shared/api"
)

// Warning handling functions

// GetWarningUUIDs returns a list of operation uuids.
func (r *ProtocolIncus) GetWarningUUIDs() ([]string, error) {
	if !r.HasExtension("warnings") {
		return nil, fmt.Errorf("The server is missing the required \"warnings\" API extension")
	}

	// Fetch the raw values.
	urls := []string{}
	baseURL := "/warnings"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetWarnings returns a list of warnings.
func (r *ProtocolIncus) GetWarnings() ([]api.Warning, error) {
	if !r.HasExtension("warnings") {
		return nil, fmt.Errorf("The server is missing the required \"warnings\" API extension")
	}

	warnings := []api.Warning{}

	_, err := r.queryStruct("GET", "/warnings?recursion=1", nil, "", &warnings)
	if err != nil {
		return nil, err
	}

	return warnings, nil
}

// GetWarning returns the warning with the given UUID.
func (r *ProtocolIncus) GetWarning(UUID string) (*api.Warning, string, error) {
	if !r.HasExtension("warnings") {
		return nil, "", fmt.Errorf("The server is missing the required \"warnings\" API extension")
	}

	warning := api.Warning{}

	etag, err := r.queryStruct("GET", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), nil, "", &warning)
	if err != nil {
		return nil, "", err
	}

	return &warning, etag, nil
}

// UpdateWarning updates the warning with the given UUID.
func (r *ProtocolIncus) UpdateWarning(UUID string, warning api.WarningPut, ETag string) error {
	if !r.HasExtension("warnings") {
		return fmt.Errorf("The server is missing the required \"warnings\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), warning, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteWarning deletes the provided warning.
func (r *ProtocolIncus) DeleteWarning(UUID string) error {
	if !r.HasExtension("warnings") {
		return fmt.Errorf("The server is missing the required \"warnings\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
