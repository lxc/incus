package main

import (
	"bufio"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/ports"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ask"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

type clusterColumn struct {
	Name string
	Data func(api.ClusterMember) string
}

type cmdCluster struct {
	global *cmdGlobal
}

func (c *cmdCluster) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("cluster")
	cmd.Short = i18n.G("Manage cluster members")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage cluster members`))

	// List
	clusterListCmd := cmdClusterList{global: c.global, cluster: c}
	cmd.AddCommand(clusterListCmd.command())

	// Rename
	clusterRenameCmd := cmdClusterRename{global: c.global, cluster: c}
	cmd.AddCommand(clusterRenameCmd.command())

	// Remove
	clusterRemoveCmd := cmdClusterRemove{global: c.global, cluster: c}
	cmd.AddCommand(clusterRemoveCmd.command())

	// Show
	clusterShowCmd := cmdClusterShow{global: c.global, cluster: c}
	cmd.AddCommand(clusterShowCmd.command())

	// Info
	clusterInfoCmd := cmdClusterInfo{global: c.global, cluster: c}
	cmd.AddCommand(clusterInfoCmd.command())

	// Get
	clusterGetCmd := cmdClusterGet{global: c.global, cluster: c}
	cmd.AddCommand(clusterGetCmd.command())

	// Set
	clusterSetCmd := cmdClusterSet{global: c.global, cluster: c}
	cmd.AddCommand(clusterSetCmd.command())

	// Unset
	clusterUnsetCmd := cmdClusterUnset{global: c.global, cluster: c, clusterSet: &clusterSetCmd}
	cmd.AddCommand(clusterUnsetCmd.command())

	// Enable
	clusterEnableCmd := cmdClusterEnable{global: c.global, cluster: c}
	cmd.AddCommand(clusterEnableCmd.command())

	// Edit
	clusterEditCmd := cmdClusterEdit{global: c.global, cluster: c}
	cmd.AddCommand(clusterEditCmd.command())

	// Join
	cmdClusterJoin := cmdClusterJoin{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterJoin.command())

	// Add token
	cmdClusterAdd := cmdClusterAdd{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterAdd.command())

	// List tokens
	cmdClusterListTokens := cmdClusterListTokens{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterListTokens.command())

	// Revoke tokens
	cmdClusterRevokeToken := cmdClusterRevokeToken{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRevokeToken.command())

	// Update certificate
	cmdClusterUpdateCertificate := cmdClusterUpdateCertificate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterUpdateCertificate.command())

	// Evacuate cluster member
	cmdClusterEvacuate := cmdClusterEvacuate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterEvacuate.command())

	// Restore cluster member
	cmdClusterRestore := cmdClusterRestore{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRestore.command())

	clusterGroupCmd := cmdClusterGroup{global: c.global, cluster: c}
	cmd.AddCommand(clusterGroupCmd.command())

	clusterRoleCmd := cmdClusterRole{global: c.global, cluster: c}
	cmd.AddCommand(clusterRoleCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }

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

var cmdClusterListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdClusterList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdClusterListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster members")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
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
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display clusters from all projects"))

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

func (c *cmdClusterList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

	filters := prepareClusterMemberServerFilters(parsed[1].StringList, api.ClusterMember{})

	// Check if clustered
	cluster, _, err := d.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
	}

	// Get the cluster members
	members, err := d.GetClusterMembersWithFilter(filters)
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

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, members)
}

// Show.
type cmdClusterShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterShowUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdClusterShowUsage...)
	cmd.Short = i18n.G("Show details of a cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show details of a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	// Get the member information
	member, _, err := d.GetClusterMember(memberName)
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

var cmdClusterInfoUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdClusterInfoUsage...)
	cmd.Short = i18n.G("Show useful information about a cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show useful information about a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	// Get the member state information.
	member, _, err := d.GetClusterMemberState(memberName)
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

var cmdClusterGetUsage = u.Usage{u.Member.Remote(), u.Key}

func (c *cmdClusterGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdClusterGetUsage...)
	cmd.Short = i18n.G("Get values for cluster member configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the member information
	member, _, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := member.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the cluster member %q: %v"), key, memberName, err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := member.Config[key]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on cluster member %q"), key, memberName)
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

var cmdClusterSetUsage = u.Usage{u.Member.Remote(), u.LegacyKV.List(1)}

func (c *cmdClusterSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdClusterSetUsage...)
	cmd.Short = i18n.G("Set a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdClusterSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	// Get the member information
	member, _, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	writable := member.Writable()
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

	return d.UpdateClusterMember(memberName, writable, "")
}

func (c *cmdClusterSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdClusterUnset struct {
	global     *cmdGlobal
	cluster    *cmdCluster
	clusterSet *cmdClusterSet

	flagIsProperty bool
}

var cmdClusterUnsetUsage = u.Usage{u.Member.Remote(), u.Key}

func (c *cmdClusterUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdClusterUnsetUsage...)
	cmd.Short = i18n.G("Unset a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.clusterSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.clusterSet, cmd, parsed)
}

// Rename.
type cmdClusterRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterRenameUsage = u.Usage{u.Member.Remote(), u.NewName(u.Member)}

func (c *cmdClusterRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdClusterRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String
	newMemberName := parsed[1].String

	// Perform the rename
	err = d.RenameClusterMember(memberName, api.ClusterMemberPost{ServerName: newMemberName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s renamed to %s")+"\n", memberName, newMemberName)
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

var cmdClusterRemoveUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdClusterRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove a member from the cluster")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove a member from the cluster`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force removing a member, even if degraded"))
	cmd.Flags().BoolVar(&c.flagNonInteractive, "yes", false, i18n.G("Don't require user confirmation for using --force"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRemove) promptConfirmation(p *u.Parsed) error {
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

Are you really sure you want to force removing %s? (yes/no): `), formatRemote(c.global.conf, p))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdClusterRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	// Prompt for confirmation if --force is used.
	if !c.flagNonInteractive && c.flagForce {
		err := c.promptConfirmation(parsed[0])
		if err != nil {
			return err
		}
	}

	// Delete the cluster member
	err = d.DeleteClusterMember(memberName, c.flagForce)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s removed")+"\n", memberName)
	}

	return nil
}

// Enable.
type cmdClusterEnable struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterEnableUsage = u.Usage{u.RemoteColonOpt, u.NewName(u.Member)}

func (c *cmdClusterEnable) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("enable", cmdClusterEnableUsage...)
	cmd.Short = i18n.G("Enable clustering on a single non-clustered server")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Enable clustering on a single non-clustered server

  This command turns a non-clustered server into the first member of a new
  cluster, which will have the given name.

  It's required that the server is already available on the network. You can check
  that by running 'incus config get core.https_address', and possibly set a value
  for the address if not yet set.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEnable) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterEnableUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[1].String

	// Check if the server is available on the network.
	server, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server config: %w"), err)
	}

	if server.Config["core.https_address"] == "" && server.Config["cluster.https_address"] == "" {
		return errors.New(i18n.G("This server is not available on the network"))
	}

	// Check if already enabled
	currentCluster, etag, err := d.GetCluster()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current cluster config: %w"), err)
	}

	if currentCluster.Enabled {
		return errors.New(i18n.G("This server is already clustered"))
	}

	// Enable clustering.
	req := api.ClusterPut{}
	req.ServerName = memberName
	req.Enabled = true
	op, err := d.UpdateCluster(req, etag)
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

var cmdClusterEditUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdClusterEditUsage...)
	cmd.Short = i18n.G("Edit cluster member configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit cluster member configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus cluster edit <cluster member> < member.yaml
    Update a cluster member using the content of member.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

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

		return d.UpdateClusterMember(memberName, newdata, "")
	}

	// Extract the current value
	member, etag, err := d.GetClusterMember(memberName)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()

	data, err := yaml.Marshal(&memberWritable)
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
		newdata := api.ClusterMemberPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = d.UpdateClusterMember(memberName, newdata, etag)
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

// Join.
type cmdClusterJoin struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterJoinUsage = u.Usage{u.MakeRemote(u.Placeholder(i18n.G("cluster")), false), u.MakeRemote(u.Member, true)}

func (c *cmdClusterJoin) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("join", cmdClusterJoinUsage...)
	cmd.Short = i18n.G("Join an existing server to a cluster")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Join an existing server to a cluster`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterJoin) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterJoinUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	cluster := parsed[0].RemoteServer
	member := parsed[1].RemoteServer
	config := newInitPressed()

	// Validate servers.
	if !cluster.IsClustered() {
		return errors.New(i18n.G("Target isn't a cluster"))
	}

	if member.IsClustered() {
		return errors.New(i18n.G("Target server is already clustered"))
	}

	// Ask the interactive questions.
	err = askClustering(c.global.asker, config, cluster, member, true)
	if err != nil {
		return err
	}

	err = fillClusterConfig(config)
	if err != nil {
		return err
	}

	if config.Cluster != nil && config.Cluster.ClusterAddress != "" && config.Cluster.ServerAddress != "" {
		err = updateCluster(member, config)
		if err != nil {
			return err
		}
	}

	return nil
}

// Add.
type cmdClusterAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterAddUsage = u.Usage{u.NewName(u.Member).Remote()}

func (c *cmdClusterAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdClusterAddUsage...)
	cmd.Short = i18n.G("Request a join token for adding a cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Request a join token for adding a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	// Request the join token.
	member := api.ClusterMembersPost{
		ServerName: memberName,
	}

	op, err := d.CreateClusterMember(member)
	if err != nil {
		return err
	}

	opAPI := op.Get()
	joinToken, err := opAPI.ToClusterJoinToken()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed converting token operation to join token: %w"), err)
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s join token:")+"\n", memberName)
	}

	fmt.Println(joinToken.String())

	return nil
}

// List Tokens.
type cmdClusterListTokens struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat  string
	flagColumns string
}

type clusterListTokenColumn struct {
	Name string
	Data func(*api.ClusterMemberJoinToken) string
}

var cmdClusterListTokensUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdClusterListTokens) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list-tokens", cmdClusterListTokensUsage...)
	cmd.Short = i18n.G("List all active cluster member join tokens")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List all active cluster member join tokens

Default column layout: ntE

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  t - Token
  E - Expires At`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable if demanded, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultclusterTokensColumns, i18n.G("Columns")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultclusterTokensColumns = "ntE"

func (c *cmdClusterListTokens) parseColumns() ([]clusterListTokenColumn, error) {
	columnsShorthandMap := map[rune]clusterListTokenColumn{
		'n': {i18n.G("NAME"), c.serverNameColumnData},
		't': {i18n.G("TOKEN"), c.tokenColumnData},
		'E': {i18n.G("EXPIRES AT"), c.expiresAtColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []clusterListTokenColumn{}

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

func (c *cmdClusterListTokens) serverNameColumnData(token *api.ClusterMemberJoinToken) string {
	return token.ServerName
}

func (c *cmdClusterListTokens) tokenColumnData(token *api.ClusterMemberJoinToken) string {
	return token.String()
}

func (c *cmdClusterListTokens) expiresAtColumnData(token *api.ClusterMemberJoinToken) string {
	return token.ExpiresAt.Local().Format(dateLayout)
}

func (c *cmdClusterListTokens) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterListTokensUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// Check if clustered.
	cluster, _, err := d.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := d.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	data := [][]string{}
	joinTokens := []*api.ClusterMemberJoinToken{}

	// Parse column flags.
	columns, err := c.parseColumns()
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

		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(joinToken))
		}

		joinTokens = append(joinTokens, joinToken)
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, joinTokens)
}

// Revoke Tokens.
type cmdClusterRevokeToken struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterRevokeTokenUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterRevokeToken) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("revoke-token", cmdClusterRevokeTokenUsage...)
	cmd.Short = i18n.G("Revoke cluster member join token")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRevokeToken) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterRevokeTokenUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	remoteName := parsed[0].RemoteName
	memberName := parsed[0].RemoteObject.String

	// Check if clustered.
	cluster, _, err := d.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := d.UseProject(api.ProjectDefaultName).GetOperations()
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

		if joinToken.ServerName == memberName {
			// Delete the operation
			err = d.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Cluster join token for %s deleted")+"\n", formatRemote(c.global.conf, parsed[0]))
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No cluster join token for member %s on remote: %s"), memberName, remoteName)
}

// Update Certificates.
type cmdClusterUpdateCertificate struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

var cmdClusterUpdateCertificateUsage = u.Usage{u.RemoteColonOpt, u.Placeholder(i18n.G("cert.crt")), u.Placeholder(i18n.G("cert.key"))}

func (c *cmdClusterUpdateCertificate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("update-certificate", cmdClusterUpdateCertificateUsage...)
	cmd.Aliases = []string{"update-cert"}
	cmd.Short = i18n.G("Update cluster certificate")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix,
		i18n.G("Update cluster certificate with PEM certificate and key read from input files."))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterUpdateCertificate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterUpdateCertificateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	remoteName := parsed[0].RemoteName
	certFile := parsed[1].String
	keyFile := parsed[2].String

	// Check if clustered.
	cluster, _, err := d.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("Server isn't part of a cluster"))
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

	err = d.UpdateClusterCertificate(certificates, "")
	if err != nil {
		return err
	}

	certf := c.global.conf.ServerCertPath(remoteName)
	if util.PathExists(certf) {
		err = os.WriteFile(certf, cert, 0o644)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not write new remote certificate for remote '%s' with error: %v"), remoteName, err)
		}
	}

	if !c.global.flagQuiet {
		fmt.Println(i18n.G("Successfully updated cluster certificates"))
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

var cmdClusterEvacuateRestoreUsage = u.Usage{u.Member.Remote()}

func (c *cmdClusterEvacuate) command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.command()
	cmd.Aliases = []string{"evac"}
	cmd.Use = cli.U("evacuate", cmdClusterEvacuateRestoreUsage...)
	cmd.Short = i18n.G("Evacuate cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Evacuate cluster member`))

	cmd.Flags().StringVar(&c.action.flagAction, "action", "", i18n.G(`Force a particular evacuation action`)+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdClusterRestore) command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.command()
	cmd.Use = cli.U("restore", cmdClusterEvacuateRestoreUsage...)
	cmd.Short = i18n.G("Restore cluster member")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Restore cluster member`))

	cmd.Flags().StringVar(&c.action.flagAction, "action", "", i18n.G(`Force a particular restoration action`)+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEvacuateAction) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagForce, "force", false, i18n.G(`Force evacuation without user confirmation`)+"``")

	return cmd
}

func (c *cmdClusterEvacuateAction) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdClusterEvacuateRestoreUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	memberName := parsed[0].RemoteObject.String

	if !c.flagForce {
		evacuate, err := c.global.asker.AskBool(fmt.Sprintf(i18n.G("Are you sure you want to %s cluster member %q? (yes/no) [default=no]: "), cmd.Name(), memberName), "no")
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

	op, err := d.UpdateClusterMemberState(memberName, state)
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

// prepareClusterMemberServerFilters processes and formats filter criteria
// for cluster members, ensuring they are in a format that the server can interpret.
func prepareClusterMemberServerFilters(filters []string, i any) []string {
	formattedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + "$"
			}

			filter = fmt.Sprintf("server_name=(%s|^%s.*)", regexpValue, key)
		} else {
			firstPart := key
			if strings.Contains(key, ".") {
				firstPart = strings.Split(key, ".")[0]
			}

			if !structHasField(reflect.TypeOf(i), firstPart) {
				filter = fmt.Sprintf("config.%s", filter)
			}
		}

		formattedFilters = append(formattedFilters, filter)
	}

	return formattedFilters
}

func newInitPressed() *api.InitPreseed {
	// Initialize config
	config := api.InitPreseed{}
	config.Config = map[string]string{}
	config.Networks = []api.InitNetworksProjectPost{}
	config.StoragePools = []api.StoragePoolsPost{}
	config.Profiles = []api.InitProfileProjectPost{
		{
			ProfilesPost: api.ProfilesPost{
				Name: "default",
				ProfilePut: api.ProfilePut{
					Config:  map[string]string{},
					Devices: map[string]map[string]string{},
				},
			},
			Project: api.ProjectDefaultName,
		},
	}

	return &config
}

func askClustering(asker ask.Asker, config *api.InitPreseed, cluster incus.InstanceServer, server incus.InstanceServer, forceJoinExisting bool) error {
	var err error

	// Setup the cluster seed data.
	config.Cluster = &api.InitClusterPreseed{}
	config.Cluster.Enabled = true

	// Get the current server's configuration.
	serverConfig, _, err := server.GetServer()
	if err != nil {
		return err
	}

	// Shared logic for server name.
	askForServerName := func() error {
		config.Cluster.ServerName, err = asker.AskString(fmt.Sprintf(i18n.G("What member name should be used to identify this server in the cluster?")+" [default=%s]: ", serverConfig.Environment.ServerName), serverConfig.Environment.ServerName, nil)
		if err != nil {
			return err
		}

		return nil
	}

	// Ask for the joining server's listen address.
	var address string
	if len(serverConfig.Environment.Addresses) > 0 {
		address, _, err = net.SplitHostPort(serverConfig.Environment.Addresses[0])
		if err != nil {
			return err
		}
	} else {
		address = internalUtil.NetworkInterfaceAddress()
	}

	validateServerAddress := func(value string) error {
		address := internalUtil.CanonicalNetworkAddress(value, ports.HTTPSDefaultPort)

		host, _, _ := net.SplitHostPort(address)
		if slices.Contains([]string{"", "[::]", "0.0.0.0"}, host) {
			return errors.New(i18n.G("Invalid IP address or DNS name"))
		}

		return nil
	}

	serverAddress, err := asker.AskString(fmt.Sprintf(i18n.G("What IP address or DNS name should be used to reach this server?")+" [default=%s]: ", address), address, validateServerAddress)
	if err != nil {
		return err
	}

	serverAddress = internalUtil.CanonicalNetworkAddress(serverAddress, ports.HTTPSDefaultPort)
	config.Config["core.https_address"] = serverAddress

	// Check if joining a cluster or creating a new one.
	clusterJoin := false
	if !forceJoinExisting {
		clusterJoin, err = asker.AskBool(i18n.G("Are you joining an existing cluster?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}
	}

	if clusterJoin || forceJoinExisting {
		// Handle joining an existing cluster.
		config.Cluster.ServerAddress = serverAddress

		// Check if we're joining a cluster during init time.
		if cluster == nil {
			// Root is required to access the certificate files
			if os.Geteuid() != 0 {
				return errors.New(i18n.G("Joining an existing cluster requires root privileges"))
			}

			// Get the join token from the user.
			var joinToken *api.ClusterMemberJoinToken

			validJoinToken := func(input string) error {
				j, err := internalUtil.JoinTokenDecode(input)
				if err != nil {
					return fmt.Errorf(i18n.G("Invalid join token: %w"), err)
				}

				joinToken = j // Store valid decoded join token
				return nil
			}

			clusterJoinToken, err := asker.AskString(i18n.G("Please provide join token:")+" ", "", validJoinToken)
			if err != nil {
				return err
			}

			// Set server name from the join token.
			config.Cluster.ServerName = joinToken.ServerName

			// Attempt to find a working cluster member to use for joining by retrieving the
			// cluster certificate from each address in the join token until we succeed.
			for _, clusterAddress := range joinToken.Addresses {
				config.Cluster.ClusterAddress = internalUtil.CanonicalNetworkAddress(clusterAddress, ports.HTTPSDefaultPort)

				// Cluster certificate
				cert, err := localtls.GetRemoteCertificate(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), version.UserAgent)
				if err != nil {
					fmt.Printf(i18n.G("Error connecting to existing cluster member %q: %v")+"\n", clusterAddress, err)
					continue
				}

				certDigest := localtls.CertFingerprint(cert)
				if joinToken.Fingerprint != certDigest {
					return fmt.Errorf(i18n.G("Certificate fingerprint mismatch between join token and cluster member %q"), clusterAddress)
				}

				config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

				break // We've found a working cluster member.
			}

			if config.Cluster.ClusterCertificate == "" {
				return errors.New(i18n.G("Unable to connect to any of the cluster members specified in join token"))
			}

			// Pass the raw join token.
			config.Cluster.ClusterToken = clusterJoinToken
		} else {
			// Ask for the server name.
			err = askForServerName()
			if err != nil {
				return err
			}

			// Get a token from the cluster.
			member := api.ClusterMembersPost{
				ServerName: config.Cluster.ServerName,
			}

			op, err := cluster.CreateClusterMember(member)
			if err != nil {
				return err
			}

			opAPI := op.Get()
			joinToken, err := opAPI.ToClusterJoinToken()
			if err != nil {
				return fmt.Errorf(i18n.G("Failed converting token operation to join token: %w"), err)
			}

			// Get cluster connection info.
			connectInfo, err := cluster.GetConnectionInfo()
			if err != nil {
				return err
			}

			// Set the token.
			config.Cluster.ClusterAddress = connectInfo.URL
			config.Cluster.ClusterCertificate = connectInfo.Certificate
			config.Cluster.ClusterToken = joinToken.String()
		}

		// Confirm wiping.
		clusterWipeMember, err := asker.AskBool(i18n.G("All existing data is lost when joining a cluster, continue?")+" (yes/no) [default=no] ", "no")
		if err != nil {
			return err
		}

		if !clusterWipeMember {
			return errors.New(i18n.G("User aborted configuration"))
		}

		// Get a client for the cluster if we weren't provided one.
		if cluster == nil {
			// Connect to existing cluster
			serverCert, err := internalUtil.LoadServerCert(internalUtil.VarPath(""))
			if err != nil {
				return err
			}

			err = setupClusterTrust(serverCert, config.Cluster.ServerName, config.Cluster.ClusterAddress, config.Cluster.ClusterCertificate, config.Cluster.ClusterToken)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to setup trust relationship with cluster: %w"), err)
			}

			// Now we have setup trust, don't send to server, otherwise it will try and setup trust
			// again and if using a one-time join token, will fail.
			config.Cluster.ClusterToken = ""

			// Client parameters to connect to the target cluster member.
			args := &incus.ConnectionArgs{
				TLSClientCert: string(serverCert.PublicKey()),
				TLSClientKey:  string(serverCert.PrivateKey()),
				TLSServerCert: string(config.Cluster.ClusterCertificate),
				UserAgent:     version.UserAgent,
			}

			client, err := incus.ConnectIncus(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), args)
			if err != nil {
				return err
			}

			cluster = client
		}

		// Get the list of required member config keys.
		clusterConfig, _, err := cluster.GetCluster()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to retrieve cluster information: %w"), err)
		}

		for i, config := range clusterConfig.MemberConfig {
			question := fmt.Sprintf(i18n.G("Choose %s:")+" ", config.Description)

			// Allow for empty values.
			configValue, err := asker.AskString(question, "", validate.Optional())
			if err != nil {
				return err
			}

			clusterConfig.MemberConfig[i].Value = configValue
		}

		config.Cluster.MemberConfig = clusterConfig.MemberConfig
	} else {
		// Ask for server name since no token is provided.
		err = askForServerName()
		if err != nil {
			return err
		}
	}

	return nil
}

func setupClusterTrust(serverCert *localtls.CertInfo, serverName string, targetAddress string, targetCert string, targetToken string) error {
	// Connect to the target cluster node.
	args := &incus.ConnectionArgs{
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := incus.ConnectIncus(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to target cluster node %q: %w"), targetAddress, err)
	}

	cert, err := localtls.GenerateTrustCertificate(serverCert, serverName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed generating trust certificate: %w"), err)
	}

	post := api.CertificatesPost{
		CertificatePut: cert.CertificatePut,
		TrustToken:     targetToken,
	}

	err = target.CreateCertificate(post)
	if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf(i18n.G("Failed to add server cert to cluster: %w"), err)
	}

	return nil
}

func fillClusterConfig(config *api.InitPreseed) error {
	// Check if the path to the cluster certificate is set
	// If yes then read cluster certificate from file
	if config.Cluster != nil && config.Cluster.ClusterCertificatePath != "" {
		if !util.PathExists(config.Cluster.ClusterCertificatePath) {
			return fmt.Errorf(i18n.G("Path %s doesn't exist"), config.Cluster.ClusterCertificatePath)
		}

		content, err := os.ReadFile(config.Cluster.ClusterCertificatePath)
		if err != nil {
			return err
		}

		config.Cluster.ClusterCertificate = string(content)
	}

	// Check if we got a cluster join token, if so, fill in the config with it.
	if config.Cluster != nil && config.Cluster.ClusterToken != "" {
		joinToken, err := internalUtil.JoinTokenDecode(config.Cluster.ClusterToken)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid cluster join token: %w"), err)
		}

		// Set server name from join token
		config.Cluster.ServerName = joinToken.ServerName

		// Attempt to find a working cluster member to use for joining by retrieving the
		// cluster certificate from each address in the join token until we succeed.
		for _, clusterAddress := range joinToken.Addresses {
			// Cluster URL
			config.Cluster.ClusterAddress = internalUtil.CanonicalNetworkAddress(clusterAddress, ports.HTTPSDefaultPort)

			// Cluster certificate
			cert, err := localtls.GetRemoteCertificate(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), version.UserAgent)
			if err != nil {
				fmt.Printf(i18n.G("Error connecting to existing cluster member %q: %v")+"\n", clusterAddress, err)
				continue
			}

			certDigest := localtls.CertFingerprint(cert)
			if joinToken.Fingerprint != certDigest {
				return fmt.Errorf(i18n.G("Certificate fingerprint mismatch between join token and cluster member %q"), clusterAddress)
			}

			config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

			break // We've found a working cluster member.
		}

		if config.Cluster.ClusterCertificate == "" {
			return errors.New(i18n.G("Unable to connect to any of the cluster members specified in join token"))
		}
	}

	// If clustering is enabled, and no cluster.https_address network address
	// was specified, we fallback to core.https_address.
	if config.Cluster != nil &&
		config.Config["core.https_address"] != "" &&
		config.Config["cluster.https_address"] == "" {
		config.Config["cluster.https_address"] = config.Config["core.https_address"]
	}

	return nil
}

func updateCluster(d incus.InstanceServer, config *api.InitPreseed) error {
	// Detect if the user has chosen to join a cluster using the new
	// cluster join API format, and use the dedicated API if so.
	if config.Cluster == nil || config.Cluster.ClusterAddress == "" || config.Cluster.ServerAddress == "" {
		return nil
	}

	// Ensure the server and cluster addresses are in canonical form.
	config.Cluster.ServerAddress = internalUtil.CanonicalNetworkAddress(config.Cluster.ServerAddress, ports.HTTPSDefaultPort)
	config.Cluster.ClusterAddress = internalUtil.CanonicalNetworkAddress(config.Cluster.ClusterAddress, ports.HTTPSDefaultPort)

	op, err := d.UpdateCluster(config.Cluster.ClusterPut, "")
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to join cluster: %w"), err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to join cluster: %w"), err)
	}

	return nil
}
