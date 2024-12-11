package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

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
	flagColumns     string
}

type networkAllocationColumn struct {
	Name string
	Data func(api.NetworkAllocations) string
}

func (c *cmdNetworkListAllocations) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-allocations")
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List network allocations in use
Default column layout: uatnm

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  u - Used by
  a - Address
  t - Type
  n - NAT
  m - Mac Address`))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.Run

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().StringVarP(&c.flagProject, "project", "p", api.ProjectDefaultName, i18n.G("Run again a specific project"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Run against all projects"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkAllocationColumns, i18n.G("Columns")+"``")

	return cmd
}

const defaultNetworkAllocationColumns = "uatnm"

func (c *cmdNetworkListAllocations) parseColumns() ([]networkAllocationColumn, error) {
	columnsShorthandMap := map[rune]networkAllocationColumn{
		'u': {i18n.G("USED BY"), c.usedByColumnData},
		'a': {i18n.G("ADDRESS"), c.addressColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'n': {i18n.G("NAT"), c.natColumnData},
		'm': {i18n.G("MAC ADDRESS"), c.macAddressColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkAllocationColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdNetworkListAllocations) usedByColumnData(alloc api.NetworkAllocations) string {
	return alloc.UsedBy
}

func (c *cmdNetworkListAllocations) addressColumnData(alloc api.NetworkAllocations) string {
	return alloc.Address
}

func (c *cmdNetworkListAllocations) typeColumnData(alloc api.NetworkAllocations) string {
	return alloc.Type
}

func (c *cmdNetworkListAllocations) natColumnData(alloc api.NetworkAllocations) string {
	strNat := "NO"
	if alloc.NAT {
		strNat = "YES"
	}

	return strNat
}

func (c *cmdNetworkListAllocations) macAddressColumnData(alloc api.NetworkAllocations) string {
	return alloc.Hwaddr
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

	var addresses []api.NetworkAllocations
	if c.flagAllProjects {
		addresses, err = server.GetNetworkAllocationsAllProjects()
		if err != nil {
			return err
		}
	} else {
		addresses, err = server.GetNetworkAllocations()
		if err != nil {
			return err
		}
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, address := range addresses {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(address))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, addresses)
}
