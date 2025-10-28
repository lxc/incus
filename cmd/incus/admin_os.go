package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus-os/incus-osd/cli"
)

// IncusOS management command.
type cmdAdminOS struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOS) Command() *cobra.Command {
	args := &cli.Args{
		SupportsTarget:    true,
		SupportsRemote:    true,
		DefaultListFormat: c.global.defaultListFormat(),
		DoHTTP: func(remoteName string, req *http.Request) (*http.Response, error) {
			if remoteName != "" && !strings.HasSuffix(remoteName, ":") {
				remoteName += ":"
			}

			// Parse the remote.
			remote, _, err := c.global.conf.ParseRemote(remoteName)
			if err != nil {
				return nil, err
			}

			// Attempt to connect.
			d, err := c.global.conf.GetInstanceServer(remote)
			if err != nil {
				return nil, err
			}

			// Get the URL prefix.
			httpInfo, err := d.GetConnectionInfo()
			if err != nil {
				return nil, err
			}

			req.URL, err = url.Parse(httpInfo.URL + req.URL.String())
			if err != nil {
				return nil, err
			}

			return d.DoHTTP(req)
		},
	}

	return cli.NewCommand(args)
}
