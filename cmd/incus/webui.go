package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdWebui struct {
	global *cmdGlobal

	flagPath string
}

var cmdWebuiUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdWebui) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("webui", cmdWebuiUsage...)
	cmd.Short = i18n.G("Open the web interface")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Open the web interface`))

	cli.AddStringFlag(cmd.Flags(), &c.flagPath, "path", "", "", i18n.G("Path to a local web UI directory"))

	cmd.RunE = c.run
	return cmd
}

func (c *cmdWebui) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdWebuiUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// Create localhost socket.
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("Unable to setup TCP socket: %w", err)
	}

	// Get the connection info.
	info, err := d.GetConnectionInfo()
	if err != nil {
		return err
	}

	uri, err := url.Parse(info.URL)
	if err != nil {
		return err
	}

	// Serve the UI locally when configured, otherwise check the remote.
	uiDir := c.resolveUIDir()
	if uiDir != "" {
		if !util.PathExists(filepath.Join(uiDir, "index.html")) {
			return fmt.Errorf(i18n.G("No web UI found at %q"), uiDir)
		}
	} else {
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/ui/", info.URL), nil)
		if err != nil {
			return err
		}

		resp, err := d.DoHTTP(req)
		if err != nil {
			return err
		}

		if resp.Body != nil {
			_ = resp.Body.Close()
		}

		if resp.StatusCode == http.StatusNotFound {
			return errors.New(i18n.G("The server doesn't have a web UI installed"))
		}
	}

	// Enable keep-alive for proxied connections.
	httpClient, err := d.GetHTTPClient()
	if err != nil {
		return err
	}

	httpTransport, ok := httpClient.Transport.(*http.Transport)
	if ok {
		httpTransport.DisableKeepAlives = false
	}

	// Get server info.
	api10, api10Etag, err := d.GetServer()
	if err != nil {
		return err
	}

	// Generate credentials.
	token := uuid.New().String()

	// Handle inbound connections.
	transport := remoteProxyTransport{
		s:       d,
		baseURL: uri,
	}

	connections := uint64(0)
	transactions := uint64(0)

	handler := remoteProxyHandler{
		s:         d,
		transport: transport,
		api10:     api10,
		api10Etag: api10Etag,
		uiDir:     uiDir,

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
		fileURL := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(redirect), "/")}
		_ = util.OpenBrowser(fileURL.String())

		// Give the browser some time to load the file.
		go func() {
			time.Sleep(time.Minute)
			_ = os.Remove(redirect)
		}()
	}

	// Start the server.
	err = http.Serve(server, &handler)
	if err != nil {
		return err
	}

	return nil
}

// resolveUIDir returns the local UI directory from the flag, INCUS_UI or the client defaults.
func (c *cmdWebui) resolveUIDir() string {
	for _, uiPath := range []string{c.flagPath, os.Getenv("INCUS_UI"), c.global.conf.Defaults.WebuiPath} {
		if uiPath != "" {
			return uiPath
		}
	}

	return ""
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
