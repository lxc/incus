package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
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
