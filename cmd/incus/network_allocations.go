package main

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
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
	lister  *listable[networkListAllocationsRowData]

	flagAllProjects bool
	flagSummary     bool
}

type networkListAllocationsRowData struct {
	api.NetworkAllocations
	subnet      string
	allocations []netip.Addr
}

var cmdNetworkListAllocationsUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkListAllocations) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list-allocations", cmdNetworkListAllocationsUsage...)
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List network allocations in use"))

	c.lister.init(cmd, "uaNtnm", map[rune]listColumn[networkListAllocationsRowData]{
		'a': {i18n.G("ADDRESS"), i18n.G("Address/subnet"), c.addressColumnData},
		'c': {i18n.G("ALLOCATED"), i18n.G("Allocated addresses (individual)"), c.allocationsColumnData},
		'C': {i18n.G("ALLOCATED"), i18n.G("Allocated addresses (ranges)"), c.allocationRangesColumnData},
		'm': {i18n.G("MAC ADDRESS"), i18n.G("MAC address"), c.macAddressColumnData},
		'n': {i18n.G("NAT"), i18n.G("Whether the network performs NAT"), c.natColumnData},
		'N': {i18n.G("NETWORK"), i18n.G("Network"), c.networkColumnData},
		's': {i18n.G("SUBNET"), i18n.G("Subnet"), c.subnetColumnData},
		't': {i18n.G("TYPE"), i18n.G("Type"), c.typeColumnData},
		'u': {i18n.G("USED BY"), i18n.G("Reference"), c.usedByColumnData},
	})

	// Column adjustments need to be performed pre-parsing.
	c.lister.preCheck(func() error {
		if c.flagSummary {
			if c.lister.columnsSpecified() {
				return errors.New(i18n.G("The --summary flag cannot be used with custom columns (-c)"))
			}

			return c.lister.adjustDefaultColumns("NsC")
		}

		return nil
	})

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.run

	cli.AddBoolFlag(cmd.Flags(), &c.flagSummary, "summary", i18n.G("Show a summary of used IP ranges per subnet"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAllProjects, "all-projects", i18n.G("Run against all projects"))

	return cmd
}

func (c *cmdNetworkListAllocations) usedByColumnData(alloc networkListAllocationsRowData) string {
	return alloc.UsedBy
}

func (c *cmdNetworkListAllocations) addressColumnData(alloc networkListAllocationsRowData) string {
	return alloc.Address
}

func (c *cmdNetworkListAllocations) networkColumnData(alloc networkListAllocationsRowData) string {
	return alloc.Network
}

func (c *cmdNetworkListAllocations) subnetColumnData(alloc networkListAllocationsRowData) string {
	return alloc.subnet
}

func (c *cmdNetworkListAllocations) typeColumnData(alloc networkListAllocationsRowData) string {
	return alloc.Type
}

func (c *cmdNetworkListAllocations) natColumnData(alloc networkListAllocationsRowData) string {
	return yesNo(alloc.NAT)
}

func (c *cmdNetworkListAllocations) macAddressColumnData(alloc networkListAllocationsRowData) string {
	return alloc.Hwaddr
}

func (c *cmdNetworkListAllocations) allocationsColumnData(alloc networkListAllocationsRowData) string {
	addrs := make([]string, len(alloc.allocations))
	for i, allocation := range sortIPs(alloc.allocations) {
		addrs[i] = allocation.String()
	}

	return strings.Join(addrs, "\n")
}

func (c *cmdNetworkListAllocations) allocationRangesColumnData(alloc networkListAllocationsRowData) string {
	return strings.Join(compactRanges(alloc.allocations), "\n")
}

func (c *cmdNetworkListAllocations) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkListAllocationsUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

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

	networks := map[string]map[netip.Prefix]*networkListAllocationsRowData{}
	var allocations []networkListAllocationsRowData
	for _, allocation := range addresses {
		row := networkListAllocationsRowData{NetworkAllocations: allocation}
		if allocation.Type == "network" {
			prefix, err := netip.ParsePrefix(allocation.Address)
			if err != nil {
				return fmt.Errorf(i18n.G("Invalid subnet %q for network %q: %w"), allocation.Address, allocation.Network, err)
			}

			row.subnet = prefix.Masked().String()
			network, ok := networks[allocation.Network]
			if ok {
				network[prefix] = &row
			} else {
				networks[allocation.Network] = map[netip.Prefix]*networkListAllocationsRowData{prefix: &row}
			}
		} else {
			allocations = append(allocations, row)
		}
	}

	external := netip.Prefix{}
	if c.lister.hasAnyColumn("cC") {
		// Do a second pass to set sub-allocations.
	out:
		for _, allocation := range allocations {
			network, ok := networks[allocation.Network]
			if !ok {
				return fmt.Errorf(i18n.G("Allocation %q from unknown network %q"), allocation.Address, allocation.Network)
			}

			prefix, err := netip.ParsePrefix(allocation.Address)
			if err != nil {
				return fmt.Errorf(i18n.G("Invalid allocation %q for network %q: %w"), allocation.Address, allocation.Network, err)
			}

			if !prefix.IsSingleIP() {
				return fmt.Errorf(i18n.G("Invalid allocation %q for network %q: %w"), allocation.Address, allocation.Network, i18n.G("Expected single IP"))
			}

			allocationIP := prefix.Addr()
			for subnet, subnetAllocation := range network {
				if subnet.Contains(allocationIP) && subnet.Bits() <= prefix.Bits() {
					subnetAllocation.allocations = append(subnetAllocation.allocations, allocationIP)
					continue out
				}
			}

			// When --summary is specified, if no matching subnet was found, put the
			// allocation under the zero prefix.
			if c.flagSummary {
				ext, ok := network[external]
				if ok {
					ext.allocations = append(ext.allocations, allocationIP)
				} else {
					network[external] = &networkListAllocationsRowData{
						NetworkAllocations: api.NetworkAllocations{Network: allocation.Network},
						subnet:             "(external)",
						allocations:        []netip.Addr{allocationIP},
					}
				}
			}
		}
	}

	rows := allocations
	if c.flagSummary {
		// When --summary is specified, ignore non-network allocations.
		rows = nil
	}

	for _, network := range networks {
		for _, allocation := range network {
			rows = append(rows, *allocation)
		}
	}

	return c.lister.run(rows, addresses)
}

func compactRanges(addrs []netip.Addr) []string {
	if len(addrs) == 0 {
		return nil
	}

	unmapped := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		unmapped = append(unmapped, a.Unmap())
	}

	addrs = sortIPs(unmapped)
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

func sortIPs(addrs []netip.Addr) []netip.Addr {
	slices.SortFunc(addrs, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(addrs)
}
