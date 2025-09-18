package incusos

// IsServiceEnabled checks if the provided service is currently enabled.
func (c *Client) IsServiceEnabled(name string) (bool, error) {
	// Get the data.
	resp, err := c.query("/services/" + name)
	if err != nil {
		return false, err
	}

	// Parse the response.
	type srv struct {
		Config struct {
			Enabled bool `json:"enabled"`
		} `json:"config"`
	}

	service := &srv{}

	err = resp.MetadataAsStruct(service)
	if err != nil {
		return false, err
	}

	return service.Config.Enabled, nil
}
