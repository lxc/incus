//go:build linux

package main

import (
	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
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

	// cluster
	adminClusterCmd := cmdAdminCluster{global: c.global}
	cmd.AddCommand(adminClusterCmd.Command())

	// init
	adminInitCmd := cmdAdminInit{global: c.global}
	cmd.AddCommand(adminInitCmd.Command())

	// recover sub-command
	adminRecoverCmd := cmdAdminRecover{global: c.global}
	cmd.AddCommand(adminRecoverCmd.Command())

	// shutdown sub-command
	shutdownCmd := cmdAdminShutdown{global: c.global}
	cmd.AddCommand(shutdownCmd.Command())

	// sql sub-command
	sqlCmd := cmdAdminSQL{global: c.global}
	cmd.AddCommand(sqlCmd.Command())

	// waitready sub-command
	adminWaitreadyCmd := cmdAdminWaitready{global: c.global}
	cmd.AddCommand(adminWaitreadyCmd.Command())

	return cmd
}
