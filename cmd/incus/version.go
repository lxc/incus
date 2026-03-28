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

var cmdVersionUsage = u.Usage{u.Colon(u.Remote).Optional()}

func (c *cmdVersion) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("version", cmdVersionUsage...)
	cmd.Short = i18n.G("Show local and remote versions")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show local and remote versions`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdVersion) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdVersionUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Client version: %s\n"), version.Version)
	ver := i18n.G("unreachable")
	resources, err := c.global.parseServers(parsed[0].String)
	if err == nil {
		resource := resources[0]
		info, _, err := resource.server.GetServer()
		if err == nil {
			ver = info.Environment.ServerVersion
		}
	}

	fmt.Printf(i18n.G("Server version: %s\n"), ver)

	return nil
}
