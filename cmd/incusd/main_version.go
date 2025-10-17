package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdVersion struct {
	global *cmdGlobal
}

func (c *cmdVersion) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "version"
	cmd.Short = "Show the server version"
	cmd.Long = cli.FormatSection("Description",
		`Show the server version`)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdVersion) run(_ *cobra.Command, _ []string) error {
	fmt.Println(version.Version)

	return nil
}
