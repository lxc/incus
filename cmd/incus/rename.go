package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
)

type cmdRename struct {
	global *cmdGlobal
}

func (c *cmdRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<instance> <instance>"))
	cmd.Short = i18n.G("Rename instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename instances`))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Check the remotes
	sourceRemote, _, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	destRemote, _, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	if sourceRemote != destRemote {
		// We just do renames
		if strings.Contains(args[1], ":") {
			return fmt.Errorf(i18n.G("Can't specify a different remote for rename"))
		}

		// Don't require the remote to be passed as both source and target
		args[1] = fmt.Sprintf("%s:%s", sourceRemote, args[1])
	}

	// Call move
	move := cmdMove{global: c.global}
	return move.Run(cmd, args)
}
