package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdClusterGroup struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

type clusterGroupColumn struct {
	Name string
	Data func(api.ClusterGroup) string
}

func (c *cmdClusterGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("group")
	cmd.Short = i18n.G("Manage cluster groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage cluster groups`))

	// Assign
	clusterGroupAssignCmd := cmdClusterGroupAssign{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupAssignCmd.command())

	// Create
	clusterGroupCreateCmd := cmdClusterGroupCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupCreateCmd.command())

	// Delete
	clusterGroupDeleteCmd := cmdClusterGroupDelete{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupDeleteCmd.command())

	// Edit
	clusterGroupEditCmd := cmdClusterGroupEdit{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupEditCmd.command())

	// List
	clusterGroupListCmd := cmdClusterGroupList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupListCmd.command())

	// Remove
	clusterGroupRemoveCmd := cmdClusterGroupRemove{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRemoveCmd.command())

	// Rename
	clusterGroupRenameCmd := cmdClusterGroupRename{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRenameCmd.command())

	// Get
	clusterGroupGetCmd := cmdClusterGroupGet{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupGetCmd.command())

	// Set
	clusterGroupSetCmd := cmdClusterGroupSet{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupSetCmd.command())

	// Unset
	clusterGroupUnsetCmd := cmdClusterGroupUnset{global: c.global, cluster: c.cluster, clusterGroupSet: &clusterGroupSetCmd}
	cmd.AddCommand(clusterGroupUnsetCmd.command())

	// Show
	clusterGroupShowCmd := cmdClusterGroupShow{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupShowCmd.command())

	// Add
	clusterGroupAddCmd := cmdClusterGroupAdd{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupAddCmd.command())

	return cmd
}

// Assign.
type cmdClusterGroupAssign struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupAssignUsage = u.Usage{u.Member.Remote(), u.Group.List(1, ",")}

func (c *cmdClusterGroupAssign) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("assign", cmdClusterGroupAssignUsage...)
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of groups to cluster members")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Assign sets of groups to cluster members`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus cluster group assign foo default,bar
    Set the groups for "foo" to "default" and "bar".

incus cluster group assign foo default
    Reset "foo" to only using the "default" cluster group.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterGroupNames(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupAssign) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupAssignUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String
	groups := parsed[1]

	member, etag, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	member.Groups = groups.StringList
	err = d.UpdateClusterMember(memberName, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s added to cluster groups %v")+"\n", formatRemote(c.global.conf, parsed[0]), groups.StringList)
	}

	return nil
}

// Create.
type cmdClusterGroupCreate struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagDescription string
}

var cmdClusterGroupCreateUsage = u.Usage{u.NewName(u.Group).Remote()}

func (c *cmdClusterGroupCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdClusterGroupCreateUsage...)
	cmd.Short = i18n.G("Create a cluster group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create a cluster group`))

	cmd.Example = cli.FormatSection("", i18n.G(`incus cluster group create g1

incus cluster group create g1 < config.yaml
	Create a cluster group with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Cluster group description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String
	var stdinData api.ClusterGroupPut

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

	// Create the cluster group
	group := api.ClusterGroupsPost{
		Name:            groupName,
		ClusterGroupPut: stdinData,
	}

	if c.flagDescription != "" {
		group.Description = c.flagDescription
	}

	err = d.CreateClusterGroup(group)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Delete.
type cmdClusterGroupDelete struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupDeleteUsage = u.Usage{u.Group.Remote().List(1)}

func (c *cmdClusterGroupDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdClusterGroupDeleteUsage...)
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete cluster groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete cluster groups`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpClusterGroups(toComplete)
	}

	return cmd
}

func (c *cmdClusterGroupDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error
	for _, p := range parsed[0].List {
		d := p.RemoteServer
		groupName := p.RemoteObject.String

		// Delete the cluster group
		err = d.DeleteClusterGroup(groupName)
		if err == nil {
			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Cluster group %s deleted")+"\n", formatRemote(c.global.conf, p))
			}
		} else {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdClusterGroupEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupEditUsage = u.Usage{u.Group.Remote()}

func (c *cmdClusterGroupEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdClusterGroupEditUsage...)
	cmd.Short = i18n.G("Edit a cluster group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit a cluster group`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterGroupPut{}

		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateClusterGroup(groupName, newdata, "")
	}

	// Extract the current value
	group, etag, err := d.GetClusterGroup(groupName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(group, yaml.V2)
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
		newdata := api.ClusterGroupPut{}

		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateClusterGroup(groupName, newdata, etag)
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

// Returns a string explaining the expected YAML structure for a cluster group configuration.
func (c *cmdClusterGroupEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the cluster group.
### Any line starting with a '# will be ignored.`)
}

// List.
type cmdClusterGroupList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat  string
	flagColumns string
}

var cmdClusterGroupListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdClusterGroupList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdClusterGroupListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster groups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List all the cluster groups

Default column layout: ndm

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  d - Description
  m - Member`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultClusterGroupColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultClusterGroupColumns = "ndm"

func (c *cmdClusterGroupList) parseColumns() ([]clusterGroupColumn, error) {
	columnsShorthandMap := map[rune]clusterGroupColumn{
		'n': {i18n.G("NAME"), c.clusterGroupNameColumnData},
		'm': {i18n.G("MEMBERS"), c.membersColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []clusterGroupColumn{}

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

func (c *cmdClusterGroupList) clusterGroupNameColumnData(group api.ClusterGroup) string {
	return group.Name
}

func (c *cmdClusterGroupList) descriptionColumnData(group api.ClusterGroup) string {
	return group.Description
}

func (c *cmdClusterGroupList) membersColumnData(group api.ClusterGroup) string {
	return fmt.Sprintf("%d", len(group.Members))
}

func (c *cmdClusterGroupList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// Check if clustered
	cluster, _, err := d.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
	}

	groups, err := d.GetClusterGroups()
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, group := range groups {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(group))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, groups)
}

// Remove.
type cmdClusterGroupRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupRemoveUsage = u.Usage{u.Member.Remote(), u.Group}

func (c *cmdClusterGroupRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdClusterGroupRemoveUsage...)
	cmd.Short = i18n.G("Remove member from group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Remove a cluster member from a cluster group`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterGroupNames(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String
	group := parsed[1].String

	// Remove the cluster group
	member, etag, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	if !slices.Contains(member.Groups, group) {
		return fmt.Errorf(i18n.G("Cluster group %s isn't currently applied to %s"), group, memberName)
	}

	groups := []string{}
	for _, g := range member.Groups {
		if g == group {
			continue
		}

		groups = append(groups, g)
	}

	member.Groups = groups

	err = d.UpdateClusterMember(memberName, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s removed from group %s")+"\n", formatRemote(c.global.conf, parsed[0]), group)
	}

	return nil
}

// Rename.
type cmdClusterGroupRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupRenameUsage = u.Usage{u.Group.Remote(), u.NewName(u.Group)}

func (c *cmdClusterGroupRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdClusterGroupRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename a cluster group`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String
	newGroupName := parsed[1].String

	// Perform the rename
	err = d.RenameClusterGroup(groupName, api.ClusterGroupPost{Name: newGroupName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newGroupName)
	}

	return nil
}

// Show.
type cmdClusterGroupShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupShowUsage = u.Usage{u.Group.Remote()}

func (c *cmdClusterGroupShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdClusterGroupShowUsage...)
	cmd.Short = i18n.G("Show cluster group configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show cluster group configurations`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String

	// Show the cluster group
	group, _, err := d.GetClusterGroup(groupName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&group, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Print(string(data))
	return nil
}

// Add.
type cmdClusterGroupAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterGroupAddUsage = u.Usage{u.Member.Remote(), u.Group}

func (c *cmdClusterGroupAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdClusterGroupAddUsage...)
	cmd.Short = i18n.G("Add member to group")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Add a cluster member to a cluster group`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterGroupNames(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String
	groupName := parsed[1].String

	// Retrieve cluster member information.
	member, etag, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	if slices.Contains(member.Groups, groupName) {
		return fmt.Errorf(i18n.G("Cluster member %s is already in group %s"), memberName, groupName)
	}

	member.Groups = append(member.Groups, groupName)

	err = d.UpdateClusterMember(memberName, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s added to group %s")+"\n", formatRemote(c.global.conf, parsed[0]), groupName)
	}

	return nil
}

// Get.
type cmdClusterGroupGet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

var cmdClusterGroupGetUsage = u.Usage{u.Group.Remote(), u.Key}

func (c *cmdClusterGroupGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdClusterGroupGetUsage...)
	cmd.Short = i18n.G("Get values for cluster group configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a cluster group property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterGroupConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the group information
	group, _, err := d.GetClusterGroup(groupName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := group.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the cluster group %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := group.Config[key]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on cluster group %q"), key, formatRemote(c.global.conf, parsed[0]))
	}

	fmt.Printf("%s\n", value)
	return nil
}

// Set.
type cmdClusterGroupSet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

var cmdClusterGroupSetUsage = u.Usage{u.Group.Remote(), u.LegacyKV.List(1)}

func (c *cmdClusterGroupSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdClusterGroupSetUsage...)
	cmd.Short = i18n.G("Set a cluster group's configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a cluster group property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdClusterGroupSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	groupName := parsed[0].RemoteObject.String

	// Get the group information
	group, _, err := d.GetClusterGroup(groupName)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	writable := group.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return d.UpdateClusterGroup(groupName, writable, "")
}

func (c *cmdClusterGroupSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdClusterGroupUnset struct {
	global          *cmdGlobal
	cluster         *cmdCluster
	clusterGroupSet *cmdClusterGroupSet

	flagIsProperty bool
}

var cmdClusterGroupUnsetUsage = u.Usage{u.Group.Remote(), u.Key}

func (c *cmdClusterGroupUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdClusterGroupUnsetUsage...)
	cmd.Short = i18n.G("Unset a cluster group's configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a cluster group property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterGroupConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGroupUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGroupUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.clusterGroupSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.clusterGroupSet, cmd, parsed)
}
