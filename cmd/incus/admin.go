//go:build linux

package main

import (
	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
)

type cmdAdmin struct {
	global *cmdGlobal
}

func (c *cmdAdmin) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("admin")
	cmd.Short = i18n.G("Manage incus daemon")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage incus daemon`))

	// init
	adminInitCmd := cmdAdminInit{global: c.global}
	cmd.AddCommand(adminInitCmd.Command())

	// waitready sub-command
	adminWaitreadyCmd := cmdAdminWaitready{global: c.global}
	cmd.AddCommand(adminWaitreadyCmd.Command())

	return cmd
}
