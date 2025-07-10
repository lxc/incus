package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroup) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("group")
	cmd.Short = i18n.G("Manage cluster groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster groups`))

	// Assign
	clusterGroupAssignCmd := cmdClusterGroupAssign{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupAssignCmd.Command())

	// Create
	clusterGroupCreateCmd := cmdClusterGroupCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupCreateCmd.Command())

	// Delete
	clusterGroupDeleteCmd := cmdClusterGroupDelete{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupDeleteCmd.Command())

	// Edit
	clusterGroupEditCmd := cmdClusterGroupEdit{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupEditCmd.Command())

	// List
	clusterGroupListCmd := cmdClusterGroupList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupListCmd.Command())

	// Remove
	clusterGroupRemoveCmd := cmdClusterGroupRemove{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRemoveCmd.Command())

	// Rename
	clusterGroupRenameCmd := cmdClusterGroupRename{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRenameCmd.Command())

	// Show
	clusterGroupShowCmd := cmdClusterGroupShow{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupShowCmd.Command())

	// Add
	clusterGroupAddCmd := cmdClusterGroupAdd{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupAddCmd.Command())

	return cmd
}

// Assign.
type cmdClusterGroupAssign struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupAssign) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("assign", i18n.G("[<remote>:]<member> <group>"))
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of groups to cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Assign sets of groups to cluster members`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus cluster group assign foo default,bar
    Set the groups for "foo" to "default" and "bar".

incus cluster group assign foo default
    Reset "foo" to only using the "default" cluster group.`))

	cmd.RunE = c.Run

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

// Run runs the actual command logic.
func (c *cmdClusterGroupAssign) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Assign the cluster group
	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if args[1] != "" {
		member.Groups = strings.Split(args[1], ",")
	} else {
		member.Groups = nil
	}

	err = resource.server.UpdateClusterMember(resource.name, member.Writable(), etag)
	if err != nil {
		return err
	}

	if args[1] == "" {
		args[1] = i18n.G("(none)")
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s added to cluster groups %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Create.
type cmdClusterGroupCreate struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagDescription string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Create a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create a cluster group`))

	cmd.Example = cli.FormatSection("", i18n.G(`incus cluster group create g1

incus cluster group create g1 < config.yaml
	Create a cluster group with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Cluster group description")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdClusterGroupCreate) Run(cmd *cobra.Command, args []string) error {
	var stdinData api.ClusterGroupPut

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster group name"))
	}

	// Create the cluster group
	group := api.ClusterGroupsPost{
		Name:            resource.name,
		ClusterGroupPut: stdinData,
	}

	if c.flagDescription != "" {
		group.Description = c.flagDescription
	}

	err = resource.server.CreateClusterGroup(group)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdClusterGroupDelete struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<group>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdClusterGroupDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster group name"))
	}

	// Delete the cluster group
	err = resource.server.DeleteClusterGroup(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdClusterGroupEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Edit a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdClusterGroupEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster group name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterGroupPut{}

		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateClusterGroup(resource.name, newdata, "")
	}

	// Extract the current value
	group, etag, err := resource.server.GetClusterGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(group)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ClusterGroupPut{}

		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateClusterGroup(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
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

	cmd.RunE = c.Run

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

// Run runs the actual command logic.
func (c *cmdClusterGroupList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if clustered
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
	}

	groups, err := resource.server.GetClusterGroups()
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member> <group>"))
	cmd.Short = i18n.G("Remove member from group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a cluster member from a cluster group`))

	cmd.RunE = c.Run

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

// Run runs the actual command logic.
func (c *cmdClusterGroupRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	// Remove the cluster group
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if !slices.Contains(member.Groups, args[1]) {
		return fmt.Errorf(i18n.G("Cluster group %s isn't currently applied to %s"), args[1], resource.name)
	}

	groups := []string{}
	for _, group := range member.Groups {
		if group == args[1] {
			continue
		}

		groups = append(groups, group)
	}

	member.Groups = groups

	err = resource.server.UpdateClusterMember(resource.name, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s removed from group %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Rename.
type cmdClusterGroupRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<group> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdClusterGroupRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Perform the rename
	err = resource.server.RenameClusterGroup(resource.name, api.ClusterGroupPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Show.
type cmdClusterGroupShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Show cluster group configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show cluster group configurations`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdClusterGroupShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster group name"))
	}

	// Show the cluster group
	group, _, err := resource.server.GetClusterGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Add.
type cmdClusterGroupAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdClusterGroupAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<member> <group>"))
	cmd.Short = i18n.G("Add member to group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add a cluster member to a cluster group`))

	cmd.RunE = c.Run

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

// Run runs the actual command logic.
func (c *cmdClusterGroupAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	// Retrieve cluster member information.
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if slices.Contains(member.Groups, args[1]) {
		return fmt.Errorf(i18n.G("Cluster member %s is already in group %s"), resource.name, args[1])
	}

	member.Groups = append(member.Groups, args[1])

	err = resource.server.UpdateClusterMember(resource.name, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s added to group %s")+"\n", resource.name, args[1])
	}

	return nil
}
