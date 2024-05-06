package main

import (
	"fmt"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
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
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.Run

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().StringVarP(&c.flagProject, "project", "p", api.ProjectDefaultName, i18n.G("Run again a specific project"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Run against all projects"))
	return cmd
}

func (c *cmdNetworkListAllocations) Run(cmd *cobra.Command, args []string) error {
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	server := resource.server.UseProject(c.flagProject)

	if c.flagAllProjects {
		addresses, err := server.GetNetworkAllocationsAllProjects()
		if err != nil {
			return err
		}

		return c.pretty(addresses)
	}

	addresses, err := server.GetNetworkAllocations()
	if err != nil {
		return err
	}

	return c.pretty(addresses)
}
