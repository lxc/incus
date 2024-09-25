package main

import (
	"fmt"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/version"
)

type cmdVersion struct {
	global *cmdGlobal
}

func (c *cmdVersion) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "version"
	cmd.Short = "Show the server version"
	cmd.Long = cli.FormatSection("Description",
		`Show the server version`)

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdVersion) Run(cmd *cobra.Command, args []string) error {
	fmt.Println(version.Version)

	return nil
}
