//go:build !windows

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/util"
)

// Run runs the actual command logic.
func (c *cmdWebui) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, _, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	s, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	// Create localhost socket.
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("Unable to setup TCP socket: %w", err)
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

	// Check that the target supports the UI.
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/ui/", info.URL), nil)
	if err != nil {
		return err
	}

	resp, err := s.DoHTTP(req)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNotFound {
		return errors.New(i18n.G("The server doesn't have a web UI installed"))
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

	// Generate credentials.
	token := uuid.New().String()

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

		token: token,
		url:   "http://" + server.Addr().String(),
	}

	// Print address.
	uiURL := fmt.Sprintf("http://%s/ui?auth_token=%s", server.Addr().String(), token)
	fmt.Printf(i18n.G("Web server running at: %s")+"\n", uiURL)

	// Attempt to automatically open the web browser, keeping the token off its command line.
	redirect, err := c.redirectFile(uiURL)
	if err == nil {
		_ = util.OpenBrowser((&url.URL{Scheme: "file", Path: redirect}).String())

		// Give the browser some time to load the file.
		go func() {
			time.Sleep(time.Minute)
			_ = os.Remove(redirect)
		}()
	}

	// Start the server.
	err = http.Serve(server, handler)
	if err != nil {
		return err
	}

	return nil
}

// redirectFile writes a private HTML file redirecting to the provided URL.
func (c *cmdWebui) redirectFile(target string) (string, error) {
	if c.global.conf.ConfigDir == "" {
		return "", errors.New("No configuration directory")
	}

	dir := c.global.conf.ConfigPath("webui")

	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp(dir, "*.html")
	if err != nil {
		return "", err
	}

	_, err = fmt.Fprintf(f, `<!DOCTYPE html><html><head><meta http-equiv="refresh" content="0;url=%s"></head></html>`+"\n", target)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return "", err
	}

	err = f.Close()
	if err != nil {
		_ = os.Remove(f.Name())

		return "", err
	}

	return f.Name(), nil
}
