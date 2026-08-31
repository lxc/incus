package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/termios"
)

type cmdNetworkPeerGroup struct {
	global *cmdGlobal
}

func (c *cmdNetworkPeerGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("peer-group")
	cmd.Short = i18n.G("Manage network peer groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage network peer groups`))

	// Create
	networkPeerGroupCreateCmd := cmdNetworkPeerGroupCreate{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupCreateCmd.command())

	// Delete
	networkPeerGroupDeleteCmd := cmdNetworkPeerGroupDelete{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupDeleteCmd.command())

	// Edit
	networkPeerGroupEditCmd := cmdNetworkPeerGroupEdit{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupEditCmd.command())

	// Join
	networkPeerGroupJoinCmd := cmdNetworkPeerGroupJoin{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupJoinCmd.command())

	// Leave
	networkPeerGroupLeaveCmd := cmdNetworkPeerGroupLeave{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupLeaveCmd.command())

	// List
	networkPeerGroupListCmd := cmdNetworkPeerGroupList{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupListCmd.command())

	// Show
	networkPeerGroupShowCmd := cmdNetworkPeerGroupShow{global: c.global, networkPeerGroup: c}
	cmd.AddCommand(networkPeerGroupShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Join.
type cmdNetworkPeerGroupJoin struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup
}

var cmdNetworkPeerGroupJoinUsage = u.Usage{u.NetworkPeerGroup.Remote(), u.MakePath(u.Target(u.Project).Optional(), u.Target(u.Network))}

func (c *cmdNetworkPeerGroupJoin) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("join", cmdNetworkPeerGroupJoinUsage...)
	cmd.Short = i18n.G("Join a network to a network peer group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Join a network to a network peer group

The network must already exist, be an OVN network, have at least one
configured subnet, and that subnet must not overlap with any other
member's subnet. Joining a network attaches its router to the peer
group and sets up routes so it can reach the group's other members.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network peer-group join region1 ovn1
    Join network ovn1 in the current project to network peer group region1

incus network peer-group join region1 web/ovn1
    Join network ovn1 in the web project to network peer group region1`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupJoin) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupJoinUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkPeerGroupName := parsed[0].RemoteObject.String
	networkProject := parsed[1].List[0].String
	networkName := parsed[1].List[1].String

	if networkProject == "" {
		networkProject = api.ProjectDefaultName
	}

	// Retrieve the current network peer group.
	networkPeerGroup, etag, err := d.GetNetworkPeerGroup(networkPeerGroupName)
	if err != nil {
		return err
	}

	if networkPeerGroupNetworksContains(networkPeerGroup.Networks, networkName, networkProject) {
		return fmt.Errorf(i18n.G("Network %s in project %s is already a member of network peer group %s"), networkName, networkProject, networkPeerGroupName)
	}

	networkPeerGroup.Networks = append(networkPeerGroup.Networks, api.NetworkPeerGroupNetwork{Name: networkName, Project: networkProject})

	err = d.UpdateNetworkPeerGroup(networkPeerGroupName, networkPeerGroup.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network %s joined to network peer group %s")+"\n", networkName, formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Leave.
type cmdNetworkPeerGroupLeave struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup
}

var cmdNetworkPeerGroupLeaveUsage = u.Usage{u.NetworkPeerGroup.Remote(), u.MakePath(u.Target(u.Project).Optional(), u.Target(u.Network))}

func (c *cmdNetworkPeerGroupLeave) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("leave", cmdNetworkPeerGroupLeaveUsage...)
	cmd.Short = i18n.G("Remove a network from a network peer group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Remove a network from a network peer group

Leaving a network peer group detaches the network's router from the
peer group and removes the routes set up for it.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network peer-group leave region1 ovn1
    Remove network ovn1 in the current project from network peer group region1

incus network peer-group leave region1 web/ovn1
    Remove network ovn1 in the web project from network peer group region1`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupLeave) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupLeaveUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkPeerGroupName := parsed[0].RemoteObject.String
	networkProject := parsed[1].List[0].String
	networkName := parsed[1].List[1].String

	if networkProject == "" {
		networkProject = api.ProjectDefaultName
	}

	// Retrieve the current network peer group.
	networkPeerGroup, etag, err := d.GetNetworkPeerGroup(networkPeerGroupName)
	if err != nil {
		return err
	}

	if !networkPeerGroupNetworksContains(networkPeerGroup.Networks, networkName, networkProject) {
		return fmt.Errorf(i18n.G("Network %s in project %s is not a member of network peer group %s"), networkName, networkProject, networkPeerGroupName)
	}

	networks := make([]api.NetworkPeerGroupNetwork, 0, len(networkPeerGroup.Networks))
	for _, n := range networkPeerGroup.Networks {
		if n.Name == networkName && n.Project == networkProject {
			continue
		}

		networks = append(networks, n)
	}

	networkPeerGroup.Networks = networks

	err = d.UpdateNetworkPeerGroup(networkPeerGroupName, networkPeerGroup.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network %s removed from network peer group %s")+"\n", networkName, formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

func networkPeerGroupNetworksContains(networks []api.NetworkPeerGroupNetwork, name string, project string) bool {
	for _, network := range networks {
		if network.Name == name && network.Project == project {
			return true
		}
	}

	return false
}

// Create.
type cmdNetworkPeerGroupCreate struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup

	flagDescription string
}

var cmdNetworkPeerGroupCreateUsage = u.Usage{u.NewName(u.NetworkPeerGroup).Remote()}

func (c *cmdNetworkPeerGroupCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkPeerGroupCreateUsage...)
	cmd.Short = i18n.G("Create network peer groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create network peer groups`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network peer-group create p1
    Create network peer group p1

incus network peer-group create p1 < config.yaml
    Create network peer group p1 with configuration from config.yaml`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagDescription, "description", "", "", i18n.G("Network peer group description"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkPeerGroupName := parsed[0].RemoteObject.String

	var stdinData api.NetworkPeerGroupPut

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		err = loader.Load(&stdinData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	// Create the network peer group
	networkPeerGroup := api.NetworkPeerGroupsPost{
		Name:                networkPeerGroupName,
		NetworkPeerGroupPut: stdinData,
	}

	if c.flagDescription != "" {
		networkPeerGroup.Description = c.flagDescription
	}

	err = d.CreateNetworkPeerGroup(networkPeerGroup)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network peer group %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Delete.
type cmdNetworkPeerGroupDelete struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup
}

var cmdNetworkPeerGroupDeleteUsage = u.Usage{u.NetworkPeerGroup.Remote().List(1)}

func (c *cmdNetworkPeerGroupDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkPeerGroupDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network peer groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete network peer groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		networkPeerGroupName := p.RemoteObject.String

		// Delete the network peer group
		err = d.DeleteNetworkPeerGroup(networkPeerGroupName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network peer group %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdNetworkPeerGroupEdit struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup
}

var cmdNetworkPeerGroupEditUsage = u.Usage{u.NetworkPeerGroup.Remote()}

func (c *cmdNetworkPeerGroupEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkPeerGroupEditUsage...)
	cmd.Short = i18n.G("Edit network peer group configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit network peer group configurations as YAML`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network peer-group edit <network peer group> < peer-group.yaml
    Update a network peer group using the content of peer-group.yaml`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network peer group.
### Any line starting with a '# will be ignored.
###
### Note that the name is shown but cannot be changed
###
### Networks must already exist, be OVN networks, have at least one
### configured subnet, and that subnet must not overlap with any other
### member's subnet. Adding a network here attaches its router to the peer
### group and sets up routes so it can reach the group's other members.`,
	)
}

func (c *cmdNetworkPeerGroupEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupEditUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkPeerGroupName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkPeerGroupPut{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateNetworkPeerGroup(networkPeerGroupName, newdata, "")
	}

	// Extract the current value
	networkPeerGroup, etag, err := d.GetNetworkPeerGroup(networkPeerGroupName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&networkPeerGroup, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.NetworkPeerGroupPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateNetworkPeerGroup(networkPeerGroupName, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = cli.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// List.
type cmdNetworkPeerGroupList struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup

	flagFormat  string
	flagColumns string
}

type networkPeerGroupColumn struct {
	Name string
	Data func(api.NetworkPeerGroup) string
}

var cmdNetworkPeerGroupListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkPeerGroupList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkPeerGroupListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List network peer groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List network peer groups

Default column layout: ndN

== Columns ==
The -c option takes a comma separated list of arguments that control
which network peer group attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
	n - Name
	d - Description
	N - Number of member networks`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", c.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`))
	cli.AddStringFlag(cmd.Flags(), &c.flagColumns, "columns|c", defaultNetworkPeerGroupColumns, "", i18n.G("Columns"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	return cmd
}

const defaultNetworkPeerGroupColumns = "ndN"

func (c *cmdNetworkPeerGroupList) parseColumns() ([]networkPeerGroupColumn, error) {
	columnsShorthandMap := map[rune]networkPeerGroupColumn{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'N': {i18n.G("NETWORKS"), c.networksColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkPeerGroupColumn{}

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

func (c *cmdNetworkPeerGroupList) nameColumnData(networkPeerGroup api.NetworkPeerGroup) string {
	return networkPeerGroup.Name
}

func (c *cmdNetworkPeerGroupList) descriptionColumnData(networkPeerGroup api.NetworkPeerGroup) string {
	return networkPeerGroup.Description
}

func (c *cmdNetworkPeerGroupList) networksColumnData(networkPeerGroup api.NetworkPeerGroup) string {
	return fmt.Sprintf("%d", len(networkPeerGroup.Networks))
}

func (c *cmdNetworkPeerGroupList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// List network peer groups
	networkPeerGroups, err := d.GetNetworkPeerGroups()
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, networkPeerGroup := range networkPeerGroups {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(networkPeerGroup))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, networkPeerGroups)
}

// Show.
type cmdNetworkPeerGroupShow struct {
	global           *cmdGlobal
	networkPeerGroup *cmdNetworkPeerGroup
}

var cmdNetworkPeerGroupShowUsage = u.Usage{u.NetworkPeerGroup.Remote()}

func (c *cmdNetworkPeerGroupShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkPeerGroupShowUsage...)
	cmd.Short = i18n.G("Show network peer group options")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show network peer group options`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkPeerGroupShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdNetworkPeerGroupShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkPeerGroupName := parsed[0].RemoteObject.String

	// Show the network peer group
	networkPeerGroup, _, err := d.GetNetworkPeerGroup(networkPeerGroupName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&networkPeerGroup, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
