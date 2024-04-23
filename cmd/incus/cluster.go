package main

import (
	"bufio"
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
	"github.com/lxc/incus/v6/shared/util"
)

type clusterColumn struct {
	Name string
	Data func(api.ClusterMember) string
}

type cmdCluster struct {
	global *cmdGlobal
}

func (c *cmdCluster) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("cluster")
	cmd.Short = i18n.G("Manage cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster members`))

	// List
	clusterListCmd := cmdClusterList{global: c.global, cluster: c}
	cmd.AddCommand(clusterListCmd.Command())

	// Rename
	clusterRenameCmd := cmdClusterRename{global: c.global, cluster: c}
	cmd.AddCommand(clusterRenameCmd.Command())

	// Remove
	clusterRemoveCmd := cmdClusterRemove{global: c.global, cluster: c}
	cmd.AddCommand(clusterRemoveCmd.Command())

	// Show
	clusterShowCmd := cmdClusterShow{global: c.global, cluster: c}
	cmd.AddCommand(clusterShowCmd.Command())

	// Info
	clusterInfoCmd := cmdClusterInfo{global: c.global, cluster: c}
	cmd.AddCommand(clusterInfoCmd.Command())

	// Get
	clusterGetCmd := cmdClusterGet{global: c.global, cluster: c}
	cmd.AddCommand(clusterGetCmd.Command())

	// Set
	clusterSetCmd := cmdClusterSet{global: c.global, cluster: c}
	cmd.AddCommand(clusterSetCmd.Command())

	// Unset
	clusterUnsetCmd := cmdClusterUnset{global: c.global, cluster: c, clusterSet: &clusterSetCmd}
	cmd.AddCommand(clusterUnsetCmd.Command())

	// Enable
	clusterEnableCmd := cmdClusterEnable{global: c.global, cluster: c}
	cmd.AddCommand(clusterEnableCmd.Command())

	// Edit
	clusterEditCmd := cmdClusterEdit{global: c.global, cluster: c}
	cmd.AddCommand(clusterEditCmd.Command())

	// Add token
	cmdClusterAdd := cmdClusterAdd{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterAdd.Command())

	// List tokens
	cmdClusterListTokens := cmdClusterListTokens{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterListTokens.Command())

	// Revoke tokens
	cmdClusterRevokeToken := cmdClusterRevokeToken{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRevokeToken.Command())

	// Update certificate
	cmdClusterUpdateCertificate := cmdClusterUpdateCertificate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterUpdateCertificate.Command())

	// Evacuate cluster member
	cmdClusterEvacuate := cmdClusterEvacuate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterEvacuate.Command())

	// Restore cluster member
	cmdClusterRestore := cmdClusterRestore{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRestore.Command())

	clusterGroupCmd := cmdClusterGroup{global: c.global, cluster: c}
	cmd.AddCommand(clusterGroupCmd.Command())

	clusterRoleCmd := cmdClusterRole{global: c.global, cluster: c}
	cmd.AddCommand(clusterRoleCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

// List.
type cmdClusterList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagColumns     string
	flagFormat      string
	flagAllProjects bool
}

func (c *cmdClusterList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all the cluster members

	The -c option takes a (optionally comma-separated) list of arguments
	that control which image attributes to output when displaying in table
	or csv format.

	Default column layout is: nurafdsm

	Column shorthand chars:

    n - Server name
    u - URL
    r - Roles
    a - Architecture
    f - Failure Domain
    d - Description
    s - Status
    m - Message`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultClusterColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display clusters from all projects"))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultClusterColumns = "nurafdsm"

func (c *cmdClusterList) parseColumns() ([]clusterColumn, error) {
	columnsShorthandMap := map[rune]clusterColumn{
		'n': {i18n.G("NAME"), c.serverColumnData},
		'u': {i18n.G("URL"), c.urlColumnData},
		'r': {i18n.G("ROLES"), c.rolesColumnData},
		'a': {i18n.G("ARCHITECTURE"), c.architectureColumnData},
		'f': {i18n.G("FAILURE DOMAIN"), c.failureDomainColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		's': {i18n.G("STATUS"), c.statusColumnData},
		'm': {i18n.G("MESSAGE"), c.messageColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []clusterColumn{}

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

func (c *cmdClusterList) serverColumnData(cluster api.ClusterMember) string {
	return cluster.ServerName
}

func (c *cmdClusterList) urlColumnData(cluster api.ClusterMember) string {
	return cluster.URL
}

func (c *cmdClusterList) rolesColumnData(cluster api.ClusterMember) string {
	roles := cluster.Roles
	rolesDelimiter := "\n"
	if c.flagFormat == "csv" {
		rolesDelimiter = ","
	}

	return strings.Join(roles, rolesDelimiter)
}

func (c *cmdClusterList) architectureColumnData(cluster api.ClusterMember) string {
	return cluster.Architecture
}

func (c *cmdClusterList) failureDomainColumnData(cluster api.ClusterMember) string {
	return cluster.FailureDomain
}

func (c *cmdClusterList) descriptionColumnData(cluster api.ClusterMember) string {
	return cluster.Description
}

func (c *cmdClusterList) statusColumnData(cluster api.ClusterMember) string {
	return strings.ToUpper(cluster.Status)
}

func (c *cmdClusterList) messageColumnData(cluster api.ClusterMember) string {
	return cluster.Message
}

func (c *cmdClusterList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	if c.global.flagProject != "" && c.flagAllProjects {
		return fmt.Errorf(i18n.G("Can't specify --project with --all-projects"))
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

	// Get the cluster members
	members, err := resource.server.GetClusterMembers()
	if err != nil {
		return err
	}

	// Process the columns
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, member := range members {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(member))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(c.flagFormat, header, data, members)
}

// Show.
type cmdClusterShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Show details of a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show details of a cluster member`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterShow) Run(cmd *cobra.Command, args []string) error {
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

	// Get the member information
	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML
	data, err := yaml.Marshal(&member)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)
	return nil
}

// Info.
type cmdClusterInfo struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Show useful information about a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about a cluster member`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterInfo) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the member state information.
	member, _, err := resource.server.GetClusterMemberState(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML.
	data, err := yaml.Marshal(&member)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)
	return nil
}

// Get.
type cmdClusterGet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<member> <key>"))
	cmd.Short = i18n.G("Get values for cluster member configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a cluster property"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterMemberConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGet) Run(cmd *cobra.Command, args []string) error {
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

	// Get the member information
	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := member.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the cluster member %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := member.Config[args[1]]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on cluster member %q"), args[1], resource.name)
	}

	fmt.Printf("%s\n", value)
	return nil
}

// Set.
type cmdClusterSet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<member> <key>=<value>..."))
	cmd.Short = i18n.G("Set a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a cluster property"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the member information
	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := member.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
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
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateClusterMember(resource.name, writable, "")
}

// Unset.
type cmdClusterUnset struct {
	global     *cmdGlobal
	cluster    *cmdCluster
	clusterSet *cmdClusterSet

	flagIsProperty bool
}

func (c *cmdClusterUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<member> <key>"))
	cmd.Short = i18n.G("Unset a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a cluster property"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterMemberConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.clusterSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.clusterSet.Run(cmd, args)
}

// Rename.
type cmdClusterRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<member> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster member`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRename) Run(cmd *cobra.Command, args []string) error {
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
	err = resource.server.RenameClusterMember(resource.name, api.ClusterMemberPost{ServerName: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Remove.
type cmdClusterRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagForce          bool
	flagNonInteractive bool
}

func (c *cmdClusterRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove a member from the cluster")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a member from the cluster`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force removing a member, even if degraded"))
	cmd.Flags().BoolVar(&c.flagNonInteractive, "yes", false, i18n.G("Don't require user confirmation for using --force"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRemove) promptConfirmation(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G(`Forcefully removing a server from the cluster should only be done as a last
resort.

The removed server will not be functional after this action and will require a
full reset, losing any remaining instance, image or storage volume that
the server may have held.

When possible, a graceful removal should be preferred, this will require you to
move any affected instance, image or storage volume to another server prior to
the server being cleanly removed from the cluster.

The --force flag should only be used if the server has died, been reinstalled
or is otherwise never expected to come back up.

Are you really sure you want to force removing %s? (yes/no): `), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdClusterRemove) Run(cmd *cobra.Command, args []string) error {
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

	// Prompt for confirmation if --force is used.
	if !c.flagNonInteractive && c.flagForce {
		err := c.promptConfirmation(resource.name)
		if err != nil {
			return err
		}
	}

	// Delete the cluster member
	err = resource.server.DeleteClusterMember(resource.name, c.flagForce)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s removed")+"\n", resource.name)
	}

	return nil
}

// Enable.
type cmdClusterEnable struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterEnable) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("enable", i18n.G("[<remote>:] <name>"))
	cmd.Short = i18n.G("Enable clustering on a single non-clustered server")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Enable clustering on a single non-clustered server

  This command turns a non-clustered server into the first member of a new
  cluster, which will have the given name.

  It's required that the server is already available on the network. You can check
  that by running 'incus config get core.https_address', and possibly set a value
  for the address if not yet set.`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEnable) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	name := args[0]
	if len(args) == 2 {
		remote = args[0]
		name = args[1]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if the server is available on the network.
	server, _, err := resource.server.GetServer()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server config: %w"), err)
	}

	if server.Config["core.https_address"] == "" && server.Config["cluster.https_address"] == "" {
		return fmt.Errorf(i18n.G("This server is not available on the network"))
	}

	// Check if already enabled
	currentCluster, etag, err := resource.server.GetCluster()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current cluster config: %w"), err)
	}

	if currentCluster.Enabled {
		return fmt.Errorf(i18n.G("This server is already clustered"))
	}

	// Enable clustering.
	req := api.ClusterPut{}
	req.ServerName = name
	req.Enabled = true
	op, err := resource.server.UpdateCluster(req, etag)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to configure cluster: %w"), err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to configure cluster: %w"), err)
	}

	fmt.Println(i18n.G("Clustering enabled"))
	return nil
}

// Edit.
type cmdClusterEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Edit cluster member configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit cluster member configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus cluster edit <cluster member> < member.yaml
    Update a cluster member using the content of member.yaml`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the cluster member.
### Any line starting with a '# will be ignored.`)
}

func (c *cmdClusterEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterMemberPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateClusterMember(resource.name, newdata, "")
	}

	// Extract the current value
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()

	data, err := yaml.Marshal(&memberWritable)
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
		newdata := api.ClusterMemberPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateClusterMember(resource.name, newdata, etag)
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

// Add.
type cmdClusterAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[[<remote>:]<member>]"))
	cmd.Short = i18n.G("Request a join token for adding a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Request a join token for adding a cluster member`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Determine the machine name.
	if resource.name == "" {
		return fmt.Errorf(i18n.G("A cluster member name must be provided"))
	}

	// Request the join token.
	member := api.ClusterMembersPost{
		ServerName: resource.name,
	}

	op, err := resource.server.CreateClusterMember(member)
	if err != nil {
		return err
	}

	opAPI := op.Get()
	joinToken, err := opAPI.ToClusterJoinToken()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed converting token operation to join token: %w"), err)
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s join token:")+"\n", resource.name)
	}

	fmt.Println(joinToken.String())

	return nil
}

// List Tokens.
type cmdClusterListTokens struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterListTokens) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-tokens", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("List all active cluster member join tokens")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`List all active cluster member join tokens`))
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

func (c *cmdClusterListTokens) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return fmt.Errorf(i18n.G("Server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	// Convert the join token operation into encoded form for display.
	type displayToken struct {
		ServerName string
		Token      string
		ExpiresAt  string
	}

	displayTokens := make([]displayToken, 0)

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToClusterJoinToken()
		if err != nil {
			continue // Operation is not a valid cluster member join token operation.
		}

		displayTokens = append(displayTokens, displayToken{
			ServerName: joinToken.ServerName,
			Token:      joinToken.String(),
			ExpiresAt:  joinToken.ExpiresAt.Local().Format(dateLayout),
		})
	}

	// Render the table.
	data := [][]string{}
	for _, token := range displayTokens {
		line := []string{token.ServerName, token.Token, token.ExpiresAt}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("TOKEN"),
		i18n.G("EXPIRES AT"),
	}

	return cli.RenderTable(c.flagFormat, header, data, displayTokens)
}

// Revoke Tokens.
type cmdClusterRevokeToken struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterRevokeToken) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("revoke-token", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Revoke cluster member join token")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRevokeToken) Run(cmd *cobra.Command, args []string) error {
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

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return fmt.Errorf(i18n.G("Server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToClusterJoinToken()
		if err != nil {
			continue // Operation is not a valid cluster member join token operation.
		}

		if joinToken.ServerName == resource.name {
			// Delete the operation
			err = resource.server.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Cluster join token for %s:%s deleted")+"\n", resource.remote, resource.name)
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No cluster join token for member %s on remote: %s"), resource.name, resource.remote)
}

// Update Certificates.
type cmdClusterUpdateCertificate struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterUpdateCertificate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("update-certificate", i18n.G("[<remote>:] <cert.crt> <cert.key>"))
	cmd.Aliases = []string{"update-cert"}
	cmd.Short = i18n.G("Update cluster certificate")
	cmd.Long = cli.FormatSection(i18n.G("Description"),
		i18n.G("Update cluster certificate with PEM certificate and key read from input files."))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		if len(args) == 2 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterUpdateCertificate) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	certFile := args[0]
	keyFile := args[1]
	if len(args) == 3 {
		remote = args[0]
		certFile = args[1]
		keyFile = args[2]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return fmt.Errorf(i18n.G("Server isn't part of a cluster"))
	}

	if !util.PathExists(certFile) {
		return fmt.Errorf(i18n.G("Could not find certificate file path: %s"), certFile)
	}

	if !util.PathExists(keyFile) {
		return fmt.Errorf(i18n.G("Could not find certificate key file path: %s"), keyFile)
	}

	cert, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate file: %s with error: %v"), certFile, err)
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate key file: %s with error: %v"), keyFile, err)
	}

	certificates := api.ClusterCertificatePut{
		ClusterCertificate:    string(cert),
		ClusterCertificateKey: string(key),
	}

	err = resource.server.UpdateClusterCertificate(certificates, "")
	if err != nil {
		return err
	}

	certf := conf.ServerCertPath(resource.remote)
	if util.PathExists(certf) {
		err = os.WriteFile(certf, cert, 0644)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not write new remote certificate for remote '%s' with error: %v"), resource.remote, err)
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Successfully updated cluster certificates for remote %s")+"\n", resource.remote)
	}

	return nil
}

type cmdClusterEvacuateAction struct {
	global *cmdGlobal

	flagAction string
	flagForce  bool
}

// Cluster member evacuation.
type cmdClusterEvacuate struct {
	global  *cmdGlobal
	cluster *cmdCluster
	action  *cmdClusterEvacuateAction
}

func (c *cmdClusterEvacuate) Command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("evacuate")
	cmd.Aliases = []string{"evac"}
	cmd.Use = usage("evacuate", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Evacuate cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Evacuate cluster member`))

	cmd.Flags().StringVar(&c.action.flagAction, "action", "", i18n.G(`Force a particular evacuation action`)+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Cluster member restore.
type cmdClusterRestore struct {
	global  *cmdGlobal
	cluster *cmdCluster
	action  *cmdClusterEvacuateAction
}

func (c *cmdClusterRestore) Command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("restore")
	cmd.Use = usage("restore", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Restore cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Restore cluster member`))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEvacuateAction) Command(action string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagForce, "force", false, i18n.G(`Force evacuation without user confirmation`)+"``")

	return cmd
}

func (c *cmdClusterEvacuateAction) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to parse servers: %w"), err)
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	if !c.flagForce {
		evacuate, err := c.global.asker.AskBool(fmt.Sprintf(i18n.G("Are you sure you want to %s cluster member %q? (yes/no) [default=no]: "), cmd.Name(), resource.name), "no")
		if err != nil {
			return err
		}

		if !evacuate {
			return nil
		}
	}

	state := api.ClusterMemberStatePost{
		Action: cmd.Name(),
		Mode:   c.flagAction,
	}

	op, err := resource.server.UpdateClusterMemberState(resource.name, state)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to update cluster member state: %w"), err)
	}

	var format string

	if cmd.Name() == "restore" {
		format = i18n.G("Restoring cluster member: %s")
	} else {
		format = i18n.G("Evacuating cluster member: %s")
	}

	progress := cli.ProgressRenderer{
		Format: format,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = op.Wait()
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")
	return nil
}
