//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdRemoteProxy struct {
	global *cmdGlobal
	remote *cmdRemote

	flagTimeout int
}

// Parse the remote without connecting so the proxy can disable keepalive first.
var cmdRemoteProxyUsage = u.Usage{u.Colon(u.Remote), u.Target(u.Placeholder(i18n.G("unix socket file")))}

func (c *cmdRemoteProxy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("proxy", cmdRemoteProxyUsage...)
	cmd.Short = i18n.G("Run a local API proxy")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Run a local API proxy for the remote`,
	))

	cmd.RunE = c.run

	cli.AddIntFlag(cmd.Flags(), &c.flagTimeout, "timeout", i18n.G("Proxy timeout (exits when no connections)"), 0)

	return cmd
}

func (c *cmdRemoteProxy) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdRemoteProxyUsage, cmd, args)
	if err != nil {
		return err
	}

	remoteName := strings.TrimSuffix(parsed[0].String, ":")
	path := parsed[1].String

	remote, ok := c.global.conf.Remotes[remoteName]
	if !ok {
		return fmt.Errorf("The remote \"%s\" doesn't exist", remoteName)
	}

	remote.KeepAlive = 0

	// Attempt to read stdin for TLS connection details.
	_ = json.NewDecoder(os.Stdin).Decode(&remote.TLS)

	c.global.conf.Remotes[remoteName] = remote

	resources, err := c.global.parseServers(parsed[0].String)
	if err != nil {
		return err
	}

	s := resources[0].server

	// Leave an existing proxy alone when it is still serving the socket.
	existing, err := net.Dial("unix", path)
	if err == nil {
		_ = existing.Close()
		return nil
	}

	// Create proxy socket.
	err = os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed to delete pre-existing unix socket: %w", err)
	}

	unixAddr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return fmt.Errorf("Unable to resolve unix socket: %w", err)
	}

	server, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return fmt.Errorf("Unable to setup unix socket: %w", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		return fmt.Errorf("Unable to set unix socket permissions: %w", err)
	}

	// Get the connection info.
	info, err := s.GetConnectionInfo()
	if err != nil {
		return err
	}

	uri, err := url.Parse(info.URL)
	if err != nil {
		return err
	}

	// Enable keep-alive for proxied connections.
	httpClient, err := s.GetHTTPClient()
	if err != nil {
		return err
	}

	httpTransport, ok := httpClient.Transport.(*http.Transport)
	if ok {
		httpTransport.DisableKeepAlives = false

		// Hold on to connections used by parallel requests, dropping them before the server's 30s idle timeout.
		httpTransport.MaxIdleConnsPerHost = 128
		httpTransport.IdleConnTimeout = 20 * time.Second

		// Keep spare TLS connections ready so websockets skip the handshake round trips.
		if uri.Scheme == "https" && httpTransport.DialTLSContext != nil {
			warm := newRemoteProxyDialer(httpTransport.DialTLSContext, 4, 128)
			httpTransport.DialTLSContext = warm.Dial
		}
	}

	// Share upstream events connections between local clients.
	events := &remoteProxyEvents{
		s:    s,
		hubs: map[string]*remoteProxyEventsHub{},
	}

	// Get server info.
	api10, api10Etag, err := s.GetServer()
	if err != nil {
		return err
	}

	// Handle inbound connections.
	transport := remoteProxyTransport{
		s:       s,
		baseURL: uri,
	}

	connections := uint64(0)
	transactions := uint64(0)

	handler := remoteProxyHandler{
		s:         s,
		transport: transport,
		api10:     api10,
		api10Etag: api10Etag,
		events:    events,

		mu:           &sync.RWMutex{},
		connections:  &connections,
		transactions: &transactions,
	}

	// Handle the timeout.
	if c.flagTimeout > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(c.flagTimeout) * time.Second)

				// Check for active connections.
				handler.mu.RLock()
				if *handler.connections > 0 {
					handler.mu.RUnlock()
					continue
				}

				// Look for recent activity
				oldCount := uint64(*handler.transactions)
				handler.mu.RUnlock()

				time.Sleep(5 * time.Second)

				handler.mu.RLock()
				if oldCount == *handler.transactions {
					handler.mu.RUnlock()

					// Daemon has been inactive for 10s, exit.
					os.Exit(0) // nolint:revive
				}

				handler.mu.RUnlock()
			}
		}()
	}

	// Start the server.
	err = http.Serve(server, &handler)
	if err != nil {
		return err
	}

	return nil
}
