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
	// init sub-command
	adminInitCmd := cmdAdminInit{global: c.global}
	cmd.AddCommand(adminInitCmd.Command())

	// shutdown sub-command
	shutdownCmd := cmdAdminShutdown{global: c.global}
	cmd.AddCommand(shutdownCmd.Command())
	// sql
	adminSqlCmd := cmdAdminSql{global: c.global}
	cmd.AddCommand(adminSqlCmd.Command())

	return cmd
}
