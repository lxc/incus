//go:build !windows

package cliconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

func (c *Config) handleKeepAlive(remote Remote, name string, args *incus.ConnectionArgs) (incus.InstanceServer, error) {
	// Create the socker directory if missing.
	socketDir := filepath.Join(c.ConfigDir, "keepalive")
	err := os.Mkdir(socketDir, 0700)
	if err != nil && !os.IsExist(err) {
		return nil, err
	}

	// Attempt to use the existing socket.
	socketPath := filepath.Join(socketDir, fmt.Sprintf("%s.socket", name))
	d, err := incus.ConnectIncusUnix(socketPath, args)
	if err != nil {
		// Delete any existing sockets.
		_ = os.Remove(socketPath)

		// Spawn the proxy.
		proc, err := subprocess.NewProcess("incus", []string{"remote", "proxy", name, socketPath, fmt.Sprintf("--timeout=%d", remote.KeepAlive)}, "", "")
		if err != nil {
			return nil, err
		}

		err = proc.Start(context.Background())
		if err != nil {
			return nil, err
		}

		// Try up to 10 times over 5s.
		for i := 0; i < 10; i++ {
			if util.PathExists(socketPath) {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		// Connect to the proxy.
		d, err = incus.ConnectIncusUnix(socketPath, args)
		if err != nil {
			return nil, err
		}
	}

	return d, nil
}
