package incusos

import (
	"errors"
	"net/http"

	osapi "github.com/lxc/incus-os/incus-osd/api"
)

// GetSystemNetwork returns the IncusOS network configuration and state.
func (c *Client) GetSystemNetwork() (*osapi.SystemNetwork, error) {
	// Get the data.
	resp, err := c.query(http.MethodGet, "/system/network")
	if err != nil {
		return nil, err
	}

	// Parse the response.
	ns := &osapi.SystemNetwork{}

	err = resp.MetadataAsStruct(ns)
	if err != nil {
		return nil, err
	}

	return ns, nil
}

// TriggerSystemUpdateCheck asks IncusOS to check for and apply any pending update.
func (c *Client) TriggerSystemUpdateCheck() error {
	// Get the data.
	resp, err := c.query(http.MethodPost, "/system/update/:check")
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return errors.New("Failed to check for updates")
	}

	return nil
}
