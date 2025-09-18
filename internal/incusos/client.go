package incusos

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/lxc/incus/v6/shared/api"
)

// Client represents an IncusOS API client.
type Client struct {
	http *http.Client
}

// NewClient instantiates a new IncusOS API client.
func NewClient() (*Client, error) {
	c := &Client{}

	c.http = &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/run/incus-os/unix.socket")
			},
		},
	}

	return c, nil
}

func (c *Client) query(path string) (*api.Response, error) {
	// Query the OS network state.
	resp, err := c.http.Get("http://incus-os/1.0" + path)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	// Convert to an Incus response struct.
	apiResp := &api.Response{}

	err = json.NewDecoder(resp.Body).Decode(apiResp)
	if err != nil {
		return nil, err
	}

	// Quick validation.
	if apiResp.Type != "sync" || apiResp.StatusCode != http.StatusOK {
		return nil, errors.New("Bad network state from IncusOS")
	}

	return apiResp, nil
}
