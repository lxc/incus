package main

import (
	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
)

type cmdWebui struct {
	global *cmdGlobal
}

// Command is a method of the cmdWebui structure that returns a new cobra Command for displaying resource usage per instance.
func (c *cmdWebui) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("webui", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("Open the web interface")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Open the web interface`))

	cmd.RunE = c.Run
	return cmd
}
