//go:build windows

package cliconfig

import (
	"fmt"

	"github.com/lxc/incus/v6/client"
)

func (c *Config) handleKeepAlive(remote Remote, name string, args *incus.ConnectionArgs) (incus.InstanceServer, error) {
	return nil, fmt.Errorf("Keepalive isn't supported on Windows")
}
