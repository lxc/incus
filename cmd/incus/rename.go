package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdRename struct {
	global *cmdGlobal
}

var cmdRenameUsage = u.Usage{u.Instance.Remote(), u.NewName(u.Instance)}

func (c *cmdRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdRenameUsage...)
	cmd.Short = i18n.G("Rename instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename instances`))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdRename) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	parsed, err := cmdRenameUsage.Parse(conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	newInstanceName := parsed[1].String

	op, err := d.RenameInstance(instanceName, api.InstancePost{Name: newInstanceName})
	if err != nil {
		return err
	}

	return op.Wait()
}
