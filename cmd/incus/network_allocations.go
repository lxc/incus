package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
	cli "github.com/lxc/incus/shared/cmd"
	"github.com/lxc/incus/internal/i18n"
)

type cmdNetworkListAllocations struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagProject     string
	flagAllProjects bool
}

func (c *cmdNetworkListAllocations) pretty(allocs []api.NetworkAllocations) error {
	header := []string{
		i18n.G("USED BY"),
		i18n.G("ADDRESS"),
		i18n.G("TYPE"),
		i18n.G("NAT"),
		i18n.G("HARDWARE ADDRESS"),
	}

	data := [][]string{}
	for _, alloc := range allocs {
		row := []string{
			alloc.UsedBy,
			alloc.Address,
			alloc.Type,
			fmt.Sprint(alloc.NAT),
			alloc.Hwaddr,
		}

		data = append(data, row)
	}

	return cli.RenderTable(c.flagFormat, header, data, allocs)
}

func (c *cmdNetworkListAllocations) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-allocations")
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List network allocations in use"))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.RunE = c.Run

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().StringVarP(&c.flagProject, "project", "p", "default", i18n.G("Run again a specific project"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Run against all projects"))
	return cmd
}

func (c *cmdNetworkListAllocations) Run(cmd *cobra.Command, args []string) error {
	d, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return nil
	}

	// Check if server is initialized.
	_, _, err = d.GetServer()
	if err != nil {
		return err
	}

	addresses, err := d.UseProject(c.flagProject).GetNetworkAllocations(c.flagAllProjects)
	if err != nil {
		return err
	}

	return c.pretty(addresses)
}
