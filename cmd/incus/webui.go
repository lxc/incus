package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdWebui struct {
	global *cmdGlobal
}

var cmdWebuiUsage = u.Usage{u.RemoteColonOpt}

// Command is a method of the cmdWebui structure that returns a new cobra Command for displaying resource usage per instance.
func (c *cmdWebui) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("webui", cmdWebuiUsage...)
	cmd.Short = i18n.G("Open the web interface")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Open the web interface`))

	cmd.RunE = c.Run
	return cmd
}
