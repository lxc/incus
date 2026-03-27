//go:build !windows

package cliconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

func (c *Config) handleKeepAlive(remote Remote, name string) (incus.InstanceServer, error) {
	// Create the socker directory if missing.
	socketDir := filepath.Join(c.ConfigDir, "keepalive")
	err := os.Mkdir(socketDir, 0o700)
	if err != nil && !os.IsExist(err) {
		return nil, err
	}

	// Attempt to use the existing socket.
	socketPath := filepath.Join(socketDir, fmt.Sprintf("%s.socket", name))
	d, err := incus.ConnectIncusUnix(socketPath, nil)
	if err != nil {
		// Delete any existing sockets.
		_ = os.Remove(socketPath)

		// Prepare to spawn the proxy.
		proc, err := subprocess.NewProcess("incus", []string{"remote", "proxy", name, socketPath, fmt.Sprintf("--timeout=%d", remote.KeepAlive)}, "", "")
		if err != nil {
			return nil, err
		}

		// Handle situation where we may need to prompt for a passphrase.
		if c.PromptPassword != nil && remote.AuthType == api.AuthenticationMethodTLS {
			tlsCert, tlsKey, tlsCA, err := c.GetClientCertificate(name)
			if err != nil {
				return nil, err
			}

			clientCert := RemoteTLS{
				Certificate: tlsCert,
				Key:         tlsKey,
				CA:          tlsCA,
			}

			out, err := json.Marshal(clientCert)
			if err != nil {
				return nil, err
			}

			proc.Stdin = io.NopCloser(bytes.NewReader(out))
		} else {
			proc.Stdin = io.NopCloser(bytes.NewReader(nil))
		}

		// Spawn the proxy.
		err = proc.Start(context.Background())
		if err != nil {
			return nil, err
		}

		// Try up to 10 times over 5s.
		for range 10 {
			if util.PathExists(socketPath) {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		// Connect to the proxy.
		d, err = incus.ConnectIncusUnix(socketPath, nil)
		if err != nil {
			return nil, err
		}
	}

	return d, nil
}
