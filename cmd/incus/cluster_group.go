package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/termios"
)

type cmdClusterGroup struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Cluster management including assignment, creation, deletion, editing, listing, removal, renaming, and showing details.
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

// Setting a groups to cluster members, setting usage, description, examples, and the RunE method.
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

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Groups assigning to a cluster member, performing checks, parsing arguments, and updating the member's group configuration.
func (c *cmdClusterGroupAssign) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Assign the cluster group
	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
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
}

// Creation of a new cluster group, defining its usage, short and long descriptions, and the RunE method.
func (c *cmdClusterGroupCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Create a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// It creates new cluster group after performing checks, parsing arguments, and making the server call for creation.
func (c *cmdClusterGroupCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
	}

	// Create the cluster group
	group := api.ClusterGroupsPost{
		Name: resource.name,
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

// It deletes a cluster group, setting up usage, descriptions, aliases, and the RunE method.
func (c *cmdClusterGroupDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<group>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// It's the deletion of a cluster group after argument checks, parsing, and making the server call for deletion.
func (c *cmdClusterGroupDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
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

// This Command generates the cobra command that enables the editing of a cluster group's attributes.
func (c *cmdClusterGroupEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Edit a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// The modification of a cluster group's configuration, either through an editor or via the terminal.
func (c *cmdClusterGroupEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
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

	flagFormat string
}

// Command returns a cobra command to list all the cluster groups in a specified format.
func (c *cmdClusterGroupList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all the cluster groups`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the command to list all the cluster groups, their descriptions, and number of members.
func (c *cmdClusterGroupList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
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
		return fmt.Errorf(i18n.G("Server isn't part of a cluster"))
	}

	groups, err := resource.server.GetClusterGroups()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, group := range groups {
		line := []string{group.Name, group.Description, fmt.Sprintf("%d", len(group.Members))}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("MEMBERS"),
	}

	return cli.RenderTable(c.flagFormat, header, data, groups)
}

// Remove.
type cmdClusterGroupRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Removal of a specified member from a specific cluster group.
func (c *cmdClusterGroupRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member> <group>"))
	cmd.Short = i18n.G("Remove member from group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a cluster member from a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// The removal process of a cluster member from a specific cluster group, with verbose output unless the 'quiet' flag is set.
func (c *cmdClusterGroupRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
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

// Renaming a cluster group, defining usage, aliases, and linking the associated runtime function.
func (c *cmdClusterGroupRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<group> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Renaming operation of a cluster group after checking arguments and parsing the remote server, and provides appropriate output.
func (c *cmdClusterGroupRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
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

// Setting up the 'show' command to display the configurations of a specified cluster group in a remote server.
func (c *cmdClusterGroupShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Show cluster group configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show cluster group configurations`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterGroups(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// This retrieves and prints the configuration details of a specified cluster group from a remote server in YAML format.
func (c *cmdClusterGroupShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
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

func (c *cmdClusterGroupAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<member> <group>"))
	cmd.Short = i18n.G("Add member to group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add a cluster member to a cluster group`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterGroupAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
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
