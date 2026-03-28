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

func (c *cmdWebui) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("webui", cmdWebuiUsage...)
	cmd.Short = i18n.G("Open the web interface")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Open the web interface`))

	cmd.RunE = c.run
	return cmd
}
