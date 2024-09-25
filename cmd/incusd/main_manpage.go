package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	cli "github.com/lxc/incus/v6/internal/cmd"
)

type cmdManpage struct {
	global *cmdGlobal
}

func (c *cmdManpage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "manpage <target>"
	cmd.Short = "Generate manpages for all commands"
	cmd.Long = cli.FormatSection("Description",
		`Generate manpages for all commands`)
	cmd.Hidden = true

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdManpage) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) != 1 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Generate the manpages
	header := &doc.GenManHeader{
		Title:   "Incus - Daemon",
		Section: "1",
	}

	opts := doc.GenManTreeOptions{
		Header:           header,
		Path:             args[0],
		CommandSeparator: ".",
	}

	_ = doc.GenManTreeFromOpts(c.global.cmd, opts)

	return nil
}
