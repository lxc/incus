package main

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdNetworkListAllocations struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagProject     string
	flagAllProjects bool
	flagColumns     string
	flagSummary     bool
}

type networkAllocationColumn struct {
	Name string
	Data func(api.NetworkAllocations) string
}

var cmdNetworkListAllocationsUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkListAllocations) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list-allocations", cmdNetworkListAllocationsUsage...)
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List network allocations in use
Default column layout: uatnm

== Columns ==
The -c option takes a comma separated list of arguments that control
which network allocations attribute attributes to output when
displaying in table or csv format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  u - Used by
  a - Address
  t - Type
  n - NAT
  m - Mac Address`,
	))

	cmd.Flags().BoolVar(&c.flagSummary, "summary", false, i18n.G("Show a summary of used IP ranges per subnet"))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.run

	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", c.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`))
	cli.AddStringFlag(cmd.Flags(), &c.flagProject, "project|p", api.ProjectDefaultName, "", i18n.G("Run again a specific project"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAllProjects, "all-projects", i18n.G("Run against all projects"))
	cli.AddStringFlag(cmd.Flags(), &c.flagColumns, "columns|c", defaultNetworkAllocationColumns, "", i18n.G("Columns"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

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

func (c *cmdNetworkListAllocations) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkListAllocationsUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	var addresses []api.NetworkAllocations
	if c.flagAllProjects {
		addresses, err = d.GetNetworkAllocationsAllProjects()
		if err != nil {
			return err
		}
	} else {
		addresses, err = d.GetNetworkAllocations()
		if err != nil {
			return err
		}
	}

	if c.flagSummary {
		if c.flagColumns != defaultNetworkAllocationColumns {
			return errors.New(i18n.G("The --summary flag cannot be used with custom columns (-c)"))
		}

		return c.renderSummary(addresses)
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

func (c *cmdNetworkListAllocations) renderSummary(allocations []api.NetworkAllocations) error {
	type netData struct {
		subnets []netip.Prefix
		used    []netip.Addr
	}

	type summaryRow struct {
		Network string `json:"network" yaml:"network"`
		Subnet  string `json:"subnet" yaml:"subnet"`
		Used    string `json:"used" yaml:"used"`
	}

	networks := make(map[string]*netData)

	for _, alloc := range allocations {
		if networks[alloc.Network] == nil {
			networks[alloc.Network] = &netData{}
		}

		if alloc.Type == "network" {
			prefix, err := netip.ParsePrefix(alloc.Address)
			if err != nil {
				return fmt.Errorf(i18n.G("Invalid subnet %q for network %q: %w"), alloc.Address, alloc.Network, err)
			}

			networks[alloc.Network].subnets = append(networks[alloc.Network].subnets, prefix.Masked())
		} else {
			ipStr := alloc.Address
			idx := strings.Index(ipStr, "/")

			if idx != -1 {
				ipStr = ipStr[:idx]
			}

			addr, err := netip.ParseAddr(ipStr)
			if err != nil {
				return fmt.Errorf(i18n.G("Invalid address %q for network %q: %w"), alloc.Address, alloc.Network, err)
			}

			networks[alloc.Network].used = append(networks[alloc.Network].used, addr)
		}
	}

	data := [][]string{}
	rows := []summaryRow{}
	headers := []string{i18n.G("NETWORK"), i18n.G("SUBNET"), i18n.G("USED")}

	var netNames []string
	for n := range networks {
		netNames = append(netNames, n)
	}

	sort.Strings(netNames)

	for _, netName := range netNames {
		netInfo := networks[netName]

		slices.SortFunc(netInfo.subnets, func(a, b netip.Prefix) int {
			c := a.Addr().Compare(b.Addr())
			if c != 0 {
				return c
			}

			return a.Bits() - b.Bits()
		})

		netInfo.subnets = slices.Compact(netInfo.subnets)

		subnetMap := make(map[string][]netip.Addr)
		for _, p := range netInfo.subnets {
			subnetMap[p.String()] = []netip.Addr{}
		}

		subnetMap["external"] = []netip.Addr{}

		for _, addr := range netInfo.used {
			matched := false
			for _, p := range netInfo.subnets {
				if p.Contains(addr) {
					subnetMap[p.String()] = append(subnetMap[p.String()], addr)
					matched = true
					break
				}
			}

			if !matched {
				subnetMap["external"] = append(subnetMap["external"], addr)
			}
		}

		for _, p := range netInfo.subnets {
			subStr := p.String()
			addrs := subnetMap[subStr]

			usedStr := strings.Join(compactRanges(addrs), ", ")

			data = append(data, []string{netName, subStr, usedStr})
			rows = append(rows, summaryRow{Network: netName, Subnet: subStr, Used: usedStr})
		}

		if len(subnetMap["external"]) > 0 {
			usedStr := strings.Join(compactRanges(subnetMap["external"]), ", ")

			data = append(data, []string{netName, "external", usedStr})
			rows = append(rows, summaryRow{Network: netName, Subnet: "external", Used: usedStr})
		}
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, headers, data, rows)
}

func compactRanges(addrs []netip.Addr) []string {
	if len(addrs) == 0 {
		return nil
	}

	unmapped := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		unmapped = append(unmapped, a.Unmap())
	}

	addrs = unmapped
	slices.SortFunc(addrs, func(a, b netip.Addr) int { return a.Compare(b) })
	addrs = slices.Compact(addrs)

	var ranges []string
	start, prev := addrs[0], addrs[0]

	flush := func() {
		if start == prev {
			ranges = append(ranges, start.String())
		} else {
			ranges = append(ranges, start.String()+"-"+prev.String())
		}
	}

	for _, a := range addrs[1:] {
		if a != prev.Next() {
			flush()
			start = a
		}

		prev = a
	}

	flush()

	return ranges
}
