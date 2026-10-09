//go:build !windows

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/shared/cliconfig"
)

func TestRemoteProxyParseWithoutConnection(t *testing.T) {
	// Keepalive stays disabled so a regressed parser cannot spawn child proxies.
	conf := &cliconfig.Config{
		DefaultRemote: "test",
		ConfigDir:     t.TempDir(),
		Remotes: map[string]cliconfig.Remote{
			"test": {
				Addrs:    []string{"https://127.0.0.1:1"},
				Protocol: "incus",
			},
		},
	}

	c := cmdRemoteProxy{global: &cmdGlobal{conf: conf}}
	cmd := c.command()
	path := filepath.Join(t.TempDir(), "proxy.socket")

	parsed, err := c.global.Parse(cmdRemoteProxyUsage, cmd, []string{"test:", path})
	require.NoError(t, err)
	require.Len(t, parsed, 2)
	assert.Equal(t, "test:", parsed[0].String)
	assert.Equal(t, path, parsed[1].String)
	assert.Nil(t, parsed[0].RemoteServer)
}

func TestRemoteProxyUnknownRemote(t *testing.T) {
	conf := &cliconfig.Config{Remotes: map[string]cliconfig.Remote{}}
	c := cmdRemoteProxy{global: &cmdGlobal{conf: conf}}
	cmd := c.command()
	path := filepath.Join(t.TempDir(), "proxy.socket")

	err := c.run(cmd, []string{"missing:", path})
	assert.EqualError(t, err, "The remote \"missing\" doesn't exist")
	assert.Empty(t, conf.Remotes)
}
