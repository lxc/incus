//go:build !linux

package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdAdmin struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdmin) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.Usage("admin")
	cmd.Short = i18n.G("Manage incus daemon")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage incus daemon`))

	return cmd
}
