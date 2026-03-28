//go:build linux

package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdAdmin struct {
	global *cmdGlobal
}

func (c *cmdAdmin) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("admin")
	cmd.Short = i18n.G("Manage incus daemon")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage incus daemon`))

	// cluster
	adminClusterCmd := cmdAdminCluster{global: c.global}
	cmd.AddCommand(adminClusterCmd.command())

	// init
	adminInitCmd := cmdAdminInit{global: c.global}
	cmd.AddCommand(adminInitCmd.command())

	// os
	adminOSCmd := cmdAdminOS{global: c.global}
	cmd.AddCommand(adminOSCmd.command())

	// recover sub-command
	adminRecoverCmd := cmdAdminRecover{global: c.global}
	cmd.AddCommand(adminRecoverCmd.command())

	// shutdown sub-command
	shutdownCmd := cmdAdminShutdown{global: c.global}
	cmd.AddCommand(shutdownCmd.command())

	// sql sub-command
	sqlCmd := cmdAdminSQL{global: c.global}
	cmd.AddCommand(sqlCmd.command())

	// waitready sub-command
	adminWaitreadyCmd := cmdAdminWaitready{global: c.global}
	cmd.AddCommand(adminWaitreadyCmd.command())

	return cmd
}
