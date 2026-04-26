package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdNetworkLoadBalancer struct {
	global     *cmdGlobal
	flagTarget string
}

func (c *cmdNetworkLoadBalancer) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("load-balancer")
	cmd.Short = i18n.G("Manage network load balancers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network load balancers"))

	// List.
	networkLoadBalancerListCmd := cmdNetworkLoadBalancerList{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerListCmd.command())

	// Show.
	networkLoadBalancerShowCmd := cmdNetworkLoadBalancerShow{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerShowCmd.command())

	// Create.
	networkLoadBalancerCreateCmd := cmdNetworkLoadBalancerCreate{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerCreateCmd.command())

	// Get.
	networkLoadBalancerGetCmd := cmdNetworkLoadBalancerGet{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerGetCmd.command())

	// Info.
	networkLoadBalancerInfoCmd := cmdNetworkLoadBalancerInfo{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerInfoCmd.command())

	// Set.
	networkLoadBalancerSetCmd := cmdNetworkLoadBalancerSet{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerSetCmd.command())

	// Unset.
	networkLoadBalancerUnsetCmd := cmdNetworkLoadBalancerUnset{global: c.global, networkLoadBalancer: c, networkLoadBalancerSet: &networkLoadBalancerSetCmd}
	cmd.AddCommand(networkLoadBalancerUnsetCmd.command())

	// Edit.
	networkLoadBalancerEditCmd := cmdNetworkLoadBalancerEdit{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerEditCmd.command())

	// Delete.
	networkLoadBalancerDeleteCmd := cmdNetworkLoadBalancerDelete{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerDeleteCmd.command())

	// Backend.
	networkLoadBalancerBackendCmd := cmdNetworkLoadBalancerBackend{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerBackendCmd.command())

	// Port.
	networkLoadBalancerPortCmd := cmdNetworkLoadBalancerPort{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerPortCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkLoadBalancerList struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagFormat  string
	flagColumns string
}

type networkLoadBalancerColumn struct {
	Name string
	Data func(api.NetworkLoadBalancer) string
}

var cmdNetworkLoadBalancerListUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkLoadBalancerList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkLoadBalancerListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network load balancers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available network load balancers

Default column layout: ldp

== Columns ==
The -c option takes a comma separated list of arguments that control
which network load balancer attributes to output when displaying
in table or csv format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  l - Listen Address
  d - Description
  p - Ports
  L - Location of the operation (e.g. its cluster member)`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkLoadBalancerColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultNetworkLoadBalancerColumns = "ldp"

func (c *cmdNetworkLoadBalancerList) parseColumns(clustered bool) ([]networkLoadBalancerColumn, error) {
	columnsShorthandMap := map[rune]networkLoadBalancerColumn{
		'l': {i18n.G("LISTEN ADDRESS"), c.listenAddressColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'p': {i18n.G("PORTS"), c.portsColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkLoadBalancerColumn{}
	if c.flagColumns == defaultNetworkLoadBalancerColumns && clustered {
		columnList = append(columnList, "L")
	}

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

func (c *cmdNetworkLoadBalancerList) listenAddressColumnData(loadBalancer api.NetworkLoadBalancer) string {
	return loadBalancer.ListenAddress
}

func (c *cmdNetworkLoadBalancerList) descriptionColumnData(loadBalancer api.NetworkLoadBalancer) string {
	return loadBalancer.Description
}

func (c *cmdNetworkLoadBalancerList) portsColumnData(loadBalancer api.NetworkLoadBalancer) string {
	return fmt.Sprintf("%d", len(loadBalancer.Ports))
}

func (c *cmdNetworkLoadBalancerList) locationColumnData(loadBalancer api.NetworkLoadBalancer) string {
	return loadBalancer.Location
}

func (c *cmdNetworkLoadBalancerList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	loadBalancers, err := d.GetNetworkLoadBalancers(networkName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, loadBalancer := range loadBalancers {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(loadBalancer))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, loadBalancers)
}

// Show.
type cmdNetworkLoadBalancerShow struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

var cmdNetworkLoadBalancerShowUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkLoadBalancerShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkLoadBalancerShowUsage...)
	cmd.Short = i18n.G("Show network load balancer configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network load balancer configurations"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Show the network load balancer config.
	loadBalancer, _, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&loadBalancer, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkLoadBalancerCreate struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
	flagDescription     string
}

var cmdNetworkLoadBalancerCreateUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.KV.List(0)}

func (c *cmdNetworkLoadBalancerCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkLoadBalancerCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network load balancers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network load balancers"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network load-balancer create n1 127.0.0.1

incus network load-balancer create n1 127.0.0.1 < config.yaml
    Create network load-balancer for network n1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Load balancer description")+"``")

	return cmd
}

func (c *cmdNetworkLoadBalancerCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	var loadBalancerPut api.NetworkLoadBalancerPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		err = loader.Load(&loadBalancerPut)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	if loadBalancerPut.Config == nil {
		loadBalancerPut.Config = map[string]string{}
	}

	maps.Copy(loadBalancerPut.Config, keys)

	// Create the network load balancer.
	loadBalancer := api.NetworkLoadBalancersPost{
		ListenAddress:          listenAddress,
		NetworkLoadBalancerPut: loadBalancerPut,
	}

	if c.flagDescription != "" {
		loadBalancer.Description = c.flagDescription
	}

	loadBalancer.Normalise()

	// If a target was specified, create the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	err = d.CreateNetworkLoadBalancer(networkName, loadBalancer)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network load balancer %s created")+"\n", loadBalancer.ListenAddress)
	}

	return nil
}

// Get.
type cmdNetworkLoadBalancerGet struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagIsProperty bool
}

var cmdNetworkLoadBalancerGetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Key}

func (c *cmdNetworkLoadBalancerGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkLoadBalancerGetUsage...)
	cmd.Short = i18n.G("Get values for network load balancer configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network load balancer configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network load balancer property"))
	return cmd
}

func (c *cmdNetworkLoadBalancerGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	key := parsed[2].String

	// Get the current config.
	loadBalancer, _, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := loadBalancer.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the load balancer %q: %v"), key, listenAddress, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range loadBalancer.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkLoadBalancerSet struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagIsProperty bool
}

var cmdNetworkLoadBalancerSetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.LegacyKV.List(1)}

func (c *cmdNetworkLoadBalancerSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkLoadBalancerSetUsage...)
	cmd.Short = i18n.G("Set network load balancer keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network load balancer keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <listen_address> <key> <value>`))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network load balancer property"))
	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkLoadBalancerSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the current config.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	if loadBalancer.Config == nil {
		loadBalancer.Config = map[string]string{}
	}

	writable := loadBalancer.Writable()
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

	writable.Normalise()

	return d.UpdateNetworkLoadBalancer(networkName, loadBalancer.ListenAddress, writable, etag)
}

func (c *cmdNetworkLoadBalancerSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkLoadBalancerUnset struct {
	global                 *cmdGlobal
	networkLoadBalancer    *cmdNetworkLoadBalancer
	networkLoadBalancerSet *cmdNetworkLoadBalancerSet

	flagIsProperty bool
}

var cmdNetworkLoadBalancerUnsetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Key}

func (c *cmdNetworkLoadBalancerUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkLoadBalancerUnsetUsage...)
	cmd.Short = i18n.G("Unset network load balancer configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network load balancer keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network load balancer property"))
	return cmd
}

func (c *cmdNetworkLoadBalancerUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkLoadBalancerSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkLoadBalancerSet, cmd, parsed)
}

// Edit.
type cmdNetworkLoadBalancerEdit struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

var cmdNetworkLoadBalancerEditUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkLoadBalancerEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkLoadBalancerEditUsage...)
	cmd.Short = i18n.G("Edit network load balancer configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network load balancer configurations as YAML"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network load balancer.
### Any line starting with a '# will be ignored.
###
### A network load balancer consists of a set of target backends and port forwards for a listen address.
###
### An example would look like:
### listen_address: 192.0.2.1
### config:
###   user.foo: bar
### description: test desc
### backends:
### - name: backend1
###   description: First backend server
###   target_address: 192.0.3.1
###   target_port: 80
### - name: backend2
###   description: Second backend server
###   target_address: 192.0.3.2
###   target_port: 80
### ports:
### - description: port forward
###   protocol: tcp
###   listen_port: 80,81,8080-8090
###   target_backend:
###    - backend1
###    - backend2
### location: server01
###
### Note that the listen_address and location cannot be changed.`)
}

func (c *cmdNetworkLoadBalancerEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		// Allow output of `incus network load-balancer show` command to be passed in here, but only take the
		// contents of the NetworkLoadBalancerPut fields when updating.
		// The other fields are silently discarded.
		newData := api.NetworkLoadBalancer{}
		err = loader.Load(&newData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		newData.Normalise()

		return d.UpdateNetworkLoadBalancer(networkName, listenAddress, newData.NetworkLoadBalancerPut, "")
	}

	// Get the current config.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&loadBalancer, yaml.V2)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newData := api.NetworkLoadBalancer{} // We show the full info, but only send the writable fields.
		err = yaml.Load(content, &newData, yaml.WithKnownFields())
		if err == nil {
			newData.Normalise()
			err = d.UpdateNetworkLoadBalancer(networkName, listenAddress, newData.Writable(), etag)
		}

		// Respawn the editor.
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

// Delete.
type cmdNetworkLoadBalancerDelete struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

var cmdNetworkLoadBalancerDeleteUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkLoadBalancerDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkLoadBalancerDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network load balancers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network load balancers"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Delete the network load balancer.
	err = d.DeleteNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network load balancer %s deleted")+"\n", listenAddress)
	}

	return nil
}

// Add/Remove Backend.
type cmdNetworkLoadBalancerBackend struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
	flagDescription     string
}

func (c *cmdNetworkLoadBalancerBackend) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("backend")
	cmd.Short = i18n.G("Manage network load balancer backends")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network load balancer backends"))

	// Backend Add.
	cmd.AddCommand(c.commandAdd())

	// Backend Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

var cmdNetworkLoadBalancerBackendAddUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.NewName(u.Backend), u.Target(u.Address), u.Target(u.Port).List(0, ",")}

func (c *cmdNetworkLoadBalancerBackend) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkLoadBalancerBackendAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add backends to a load balancer")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add backend to a load balancer"))
	cmd.RunE = c.runAdd

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Backend description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerBackend) runAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerBackendAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	backendName := parsed[2].String
	targetAddress := parsed[3].String
	// Only the list’s string representation is used.
	targetPorts := parsed[4].String

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	loadBalancer.Backends = append(loadBalancer.Backends, api.NetworkLoadBalancerBackend{
		Name:          backendName,
		TargetAddress: targetAddress,
		TargetPort:    targetPorts,
		Description:   c.flagDescription,
	})

	loadBalancer.Normalise()

	return d.UpdateNetworkLoadBalancer(networkName, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

var cmdNetworkLoadBalancerBackendRemoveUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Backend}

func (c *cmdNetworkLoadBalancerBackend) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkLoadBalancerBackendRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove backends from a load balancer")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove backend from a load balancer"))
	cmd.RunE = c.runRemove

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerBackend) runRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerBackendRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	backendName := parsed[2].String

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	removed := false
	newBackends := make([]api.NetworkLoadBalancerBackend, 0, len(loadBalancer.Backends))

	for _, backend := range loadBalancer.Backends {
		if backend.Name == backendName {
			removed = true
			continue // Don't add removed backend to newBackends.
		}

		newBackends = append(newBackends, backend)
	}

	if !removed {
		return errors.New(i18n.G("No matching backend found"))
	}

	loadBalancer.Backends = newBackends
	loadBalancer.Normalise()

	return d.UpdateNetworkLoadBalancer(networkName, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

// Add/Remove Port.
type cmdNetworkLoadBalancerPort struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
	flagRemoveForce     bool
	flagDescription     string
}

func (c *cmdNetworkLoadBalancerPort) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("port")
	cmd.Short = i18n.G("Manage network load balancer ports")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network load balancer ports"))

	// Port Add.
	cmd.AddCommand(c.commandAdd())

	// Port Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

var cmdNetworkLoadBalancerPortAddUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Protocol, u.ListenPort.List(1, ","), u.Backend.List(1, ",")}

func (c *cmdNetworkLoadBalancerPort) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkLoadBalancerPortAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add ports to a load balancer")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add ports to a load balancer"))
	cmd.RunE = c.runAdd

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Port description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerPort) runAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerPortAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	protocol := parsed[2].String
	// Only the list’s string representation is used.
	listenPorts := parsed[3].String
	backends := parsed[4].StringList

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	loadBalancer.Ports = append(loadBalancer.Ports, api.NetworkLoadBalancerPort{
		Protocol:      protocol,
		ListenPort:    listenPorts,
		TargetBackend: backends,
		Description:   c.flagDescription,
	})

	loadBalancer.Normalise()

	return d.UpdateNetworkLoadBalancer(networkName, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

var cmdNetworkLoadBalancerPortRemoveUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Protocol.Optional(u.ListenPort.List(0, ","))}

func (c *cmdNetworkLoadBalancerPort) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkLoadBalancerPortRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove ports from a load balancer")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove ports from a load balancer"))
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, i18n.G("Remove all ports that match"))
	cmd.RunE = c.runRemove

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerPort) runRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerPortRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	hasProtocol := !parsed[2].Skipped
	protocol := ""
	hasListenPorts := false
	listenPorts := ""
	if hasProtocol {
		protocol = parsed[2].List[0].String
		hasListenPorts = !parsed[2].List[1].Skipped
		// Only the list’s string representation is used.
		listenPorts = parsed[2].List[1].String
	}

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		d = d.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := d.GetNetworkLoadBalancer(networkName, listenAddress)
	if err != nil {
		return err
	}

	removed := false
	newPorts := make([]api.NetworkLoadBalancerPort, 0, len(loadBalancer.Ports))

	for _, port := range loadBalancer.Ports {
		if hasProtocol && port.Protocol != protocol || hasListenPorts && port.ListenPort != listenPorts {
			newPorts = append(newPorts, port)
		} else {
			if removed && !c.flagRemoveForce {
				return errors.New(i18n.G("Multiple ports match. Use --force to remove them all"))
			}

			removed = true
		}
	}

	if !removed {
		return errors.New(i18n.G("No matching port(s) found"))
	}

	loadBalancer.Ports = newPorts
	loadBalancer.Normalise()

	return d.UpdateNetworkLoadBalancer(networkName, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

// Info.
type cmdNetworkLoadBalancerInfo struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

var cmdNetworkLoadBalancerInfoUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkLoadBalancerInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdNetworkLoadBalancerInfoUsage...)
	cmd.Short = i18n.G("Get current load balancer status")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get current load-balancer status"))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkLoadBalancerInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkLoadBalancerInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// Get the load-balancer state.
	lbState, err := d.GetNetworkLoadBalancerState(networkName, listenAddress)
	if err != nil {
		return err
	}

	// Render the state.
	if lbState.BackendHealth == nil {
		// Currently the only field in the state endpoint is the backend health, fail if it's missing.
		return errors.New(i18n.G("No load-balancer health information available"))
	}

	fmt.Println(i18n.G("Backend health:"))
	for backend, info := range lbState.BackendHealth {
		if len(info.Ports) == 0 {
			continue
		}

		fmt.Printf("  %s (%s):\n", backend, info.Address)
		for _, port := range info.Ports {
			fmt.Printf("    - %s/%d: %s\n", port.Protocol, port.Port, port.Status)
		}

		fmt.Println("")
	}

	return nil
}
