package incusos

import (
	osapi "github.com/lxc/incus-os/incus-osd/api"
)

// GetSystemNetwork returns the IncusOS network configuration and state.
func (c *Client) GetSystemNetwork() (*osapi.SystemNetwork, error) {
	// Get the data.
	resp, err := c.query("/system/network")
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
