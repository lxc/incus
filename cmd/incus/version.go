package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/version"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdVersion struct {
	global *cmdGlobal
}

var cmdVersionUsage = u.Usage{u.RemoteColonOpt}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdVersion) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("version", cmdVersionUsage...)
	cmd.Short = i18n.G("Show local and remote versions")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show local and remote versions`))

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdVersion) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdVersionUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	fmt.Printf(i18n.G("Client version: %s\n"), version.Version)
	ver := i18n.G("unreachable")
	info, _, err := d.GetServer()
	if err == nil {
		ver = info.Environment.ServerVersion
	}

	fmt.Printf(i18n.G("Server version: %s\n"), ver)

	return nil
}
