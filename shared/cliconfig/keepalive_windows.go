//go:build windows

package cliconfig

import (
	"errors"

	incus "github.com/lxc/incus/v6/client"
)

func (c *Config) handleKeepAlive(remote Remote, name string, args *incus.ConnectionArgs) (incus.InstanceServer, error) {
	return nil, errors.New("Keepalive isn't supported on Windows")
}
