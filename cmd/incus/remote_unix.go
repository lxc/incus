//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

type cmdRemoteProxy struct {
	global *cmdGlobal
	remote *cmdRemote

	flagTimeout int
}

func (c *cmdRemoteProxy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("proxy", i18n.G("<remote>: <path>"))
	cmd.Short = i18n.G("Run a local API proxy")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Run a local API proxy for the remote`))

	cmd.RunE = c.Run

	cmd.Flags().IntVar(&c.flagTimeout, "timeout", 0, i18n.G("Proxy timeout (exits when no connections)")+"``")

	return cmd
}

func (c *cmdRemoteProxy) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Detect remote name.
	remoteName := args[0]
	if !strings.HasSuffix(remoteName, ":") {
		remoteName = remoteName + ":"
	}

	path := args[1]

	remote := c.global.conf.Remotes[strings.TrimSuffix(remoteName, ":")]
	remote.KeepAlive = 0
	c.global.conf.Remotes[strings.TrimSuffix(remoteName, ":")] = remote

	resources, err := c.global.ParseServers(remoteName)
	if err != nil {
		return err
	}

	s := resources[0].server

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

	err = os.Chmod(path, 0600)
	if err != nil {
		return fmt.Errorf("Unable to set socket permissions: %w", err)
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
					os.Exit(0)
				}

				handler.mu.RUnlock()
			}
		}()
	}

	// Start the server.
	err = http.Serve(server, handler)
	if err != nil {
		return err
	}

	return nil
}

type remoteProxyTransport struct {
	s incus.InstanceServer

	baseURL *url.URL
}

func (t remoteProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Fix the request.
	r.URL.Scheme = t.baseURL.Scheme
	r.URL.Host = t.baseURL.Host
	r.RequestURI = ""

	return t.s.DoHTTP(r)
}

type remoteProxyHandler struct {
	s         incus.InstanceServer
	transport http.RoundTripper

	mu           *sync.RWMutex
	connections  *uint64
	transactions *uint64

	api10     *api.Server
	api10Etag string
}

func (h remoteProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Increase counters.
	defer func() {
		h.mu.Lock()
		*h.connections -= 1
		h.mu.Unlock()
	}()

	h.mu.Lock()
	*h.transactions += 1
	*h.connections += 1
	h.mu.Unlock()

	// Handle /1.0 internally (saves a round-trip).
	if r.RequestURI == "/1.0" || strings.HasPrefix(r.RequestURI, "/1.0?project=") {
		// Parse query URL.
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return
		}

		// Update project name to match.
		projectName := values.Get("project")
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}

		api10 := api.Server(*h.api10)
		api10.Environment.Project = projectName

		// Set the request headers.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", h.api10Etag)
		w.WriteHeader(http.StatusOK)

		// Generate a body from the cached data.
		serverBody, err := json.Marshal(api10)
		if err != nil {
			return
		}

		apiResponse := api.Response{
			Type:       "sync",
			Status:     "success",
			StatusCode: 200,
			Metadata:   serverBody,
		}

		body, err := json.Marshal(apiResponse)
		if err != nil {
			return
		}

		_, _ = w.Write(body)

		return
	}

	// Forward everything else.
	proxy := httputil.ReverseProxy{
		Transport: h.transport,
		Director:  func(*http.Request) {},
	}

	proxy.ServeHTTP(w, r)
}
