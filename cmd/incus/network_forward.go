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

type cmdNetworkForward struct {
	global     *cmdGlobal
	flagTarget string
}

func (c *cmdNetworkForward) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("forward")
	cmd.Short = i18n.G("Manage network forwards")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network forwards"))

	// List.
	networkForwardListCmd := cmdNetworkForwardList{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardListCmd.command())

	// Show.
	networkForwardShowCmd := cmdNetworkForwardShow{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardShowCmd.command())

	// Create.
	networkForwardCreateCmd := cmdNetworkForwardCreate{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardCreateCmd.command())

	// Get.
	networkForwardGetCmd := cmdNetworkForwardGet{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardGetCmd.command())

	// Set.
	networkForwardSetCmd := cmdNetworkForwardSet{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardSetCmd.command())

	// Unset.
	networkForwardUnsetCmd := cmdNetworkForwardUnset{global: c.global, networkForward: c, networkForwardSet: &networkForwardSetCmd}
	cmd.AddCommand(networkForwardUnsetCmd.command())

	// Edit.
	networkForwardEditCmd := cmdNetworkForwardEdit{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardEditCmd.command())

	// Delete.
	networkForwardDeleteCmd := cmdNetworkForwardDelete{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardDeleteCmd.command())

	// Port.
	networkForwardPortCmd := cmdNetworkForwardPort{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardPortCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkForwardList struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagFormat  string
	flagColumns string
}

type networkForwardColumn struct {
	Name string
	Data func(api.NetworkForward) string
}

var cmdNetworkForwardListUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkForwardList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkForwardListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network forwards")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available network forwards

Default column layout: ldDp

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
l - Listen Address
d - Description
D - Default Target Address
p - Port
L - Location of the network zone (e.g. its cluster member)`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkForwardColumns, i18n.G("Columns")+"``")

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

const defaultNetworkForwardColumns = "ldDp"

func (c *cmdNetworkForwardList) parseColumns(clustered bool) ([]networkForwardColumn, error) {
	columnsShorthandMap := map[rune]networkForwardColumn{
		'l': {i18n.G("LISTEN ADDRESS"), c.listenAddressColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'D': {i18n.G("DEFAULT TARGET ADDRESS"), c.defaultTargetAddressColumnData},
		'p': {i18n.G("PORTS"), c.portsColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkForwardColumn{}
	if c.flagColumns == defaultNetworkForwardColumns && clustered {
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

func (c *cmdNetworkForwardList) listenAddressColumnData(forward api.NetworkForward) string {
	return forward.ListenAddress
}

func (c *cmdNetworkForwardList) descriptionColumnData(forward api.NetworkForward) string {
	return forward.Description
}

func (c *cmdNetworkForwardList) defaultTargetAddressColumnData(forward api.NetworkForward) string {
	return forward.Config["target_address"]
}

func (c *cmdNetworkForwardList) portsColumnData(forward api.NetworkForward) string {
	return fmt.Sprintf("%d", len(forward.Ports))
}

func (c *cmdNetworkForwardList) locationColumnData(forward api.NetworkForward) string {
	return forward.Location
}

func (c *cmdNetworkForwardList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	forwards, err := d.GetNetworkForwards(networkName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	data := make([][]string, 0, len(forwards))
	for _, forward := range forwards {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(forward))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, forwards)
}

// Show.
type cmdNetworkForwardShow struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

var cmdNetworkForwardShowUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkForwardShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkForwardShowUsage...)
	cmd.Short = i18n.G("Show network forward configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network forward configurations"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// Show the network forward config.
	forward, _, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&forward, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkForwardCreate struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagDescription string
}

var cmdNetworkForwardCreateUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.KV.List(0)}

func (c *cmdNetworkForwardCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkForwardCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network forwards")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network forwards"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network forward create n1 127.0.0.1

incus network forward create n1 127.0.0.1 < config.yaml
    Create a new network forward for network n1 from config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Network forward description")+"``")

	return cmd
}

func (c *cmdNetworkForwardCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardCreateUsage.Parse(c.global.conf, cmd, args)
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
	var forwardPut api.NetworkForwardPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		err = loader.Load(&forwardPut)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	if forwardPut.Config == nil {
		forwardPut.Config = map[string]string{}
	}

	maps.Copy(forwardPut.Config, keys)

	// Create the network forward.
	forward := api.NetworkForwardsPost{
		ListenAddress:     listenAddress,
		NetworkForwardPut: forwardPut,
	}

	if c.flagDescription != "" {
		forward.Description = c.flagDescription
	}

	forward.Normalise()

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	err = d.CreateNetworkForward(networkName, forward)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network forward %s created")+"\n", forward.ListenAddress)
	}

	return nil
}

// Get.
type cmdNetworkForwardGet struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagIsProperty bool
}

var cmdNetworkForwardGetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Key}

func (c *cmdNetworkForwardGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkForwardGetUsage...)
	cmd.Short = i18n.G("Get values for network forward configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network forward configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network forward property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkForwardConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	key := parsed[2].String

	// Get the current config.
	forward, _, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := forward.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network forward %q: %v"), key, listenAddress, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range forward.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkForwardSet struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagIsProperty bool
}

var cmdNetworkForwardSetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.LegacyKV.List(1)}

func (c *cmdNetworkForwardSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkForwardSetUsage...)
	cmd.Short = i18n.G("Set network forward keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network forward keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <listen_address> <key> <value>`))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network forward property"))
	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkForwardSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// Get the current config.
	forward, etag, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	if forward.Config == nil {
		forward.Config = map[string]string{}
	}

	writable := forward.Writable()
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

	return d.UpdateNetworkForward(networkName, forward.ListenAddress, writable, etag)
}

func (c *cmdNetworkForwardSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkForwardUnset struct {
	global            *cmdGlobal
	networkForward    *cmdNetworkForward
	networkForwardSet *cmdNetworkForwardSet

	flagIsProperty bool
}

var cmdNetworkForwardUnsetUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Key}

func (c *cmdNetworkForwardUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkForwardUnsetUsage...)
	cmd.Short = i18n.G("Unset network forward configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network forward keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network forward property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkForwardConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkForwardSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkForwardSet, cmd, parsed)
}

// Edit.
type cmdNetworkForwardEdit struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

var cmdNetworkForwardEditUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkForwardEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkForwardEditUsage...)
	cmd.Short = i18n.G("Edit network forward configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network forward configurations as YAML"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network forward.
### Any line starting with a '# will be ignored.
###
### A network forward consists of a default target address and optional set of port forwards for a listen address.
###
### An example would look like:
### listen_address: 192.0.2.1
### config:
###   target_address: 198.51.100.2
### description: test desc
### ports:
### - description: port forward
###   protocol: tcp
###   listen_port: 80,81,8080-8090
###   target_address: 198.51.100.3
###   target_port: 80,81,8080-8090
### location: server01
###
### Note that the listen_address and location cannot be changed.`)
}

func (c *cmdNetworkForwardEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		// Allow output of `incus network forward show` command to be passed in here, but only take the
		// contents of the NetworkForwardPut fields when updating. The other fields are silently discarded.
		newData := api.NetworkForward{}
		err = loader.Load(&newData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		newData.Normalise()

		return d.UpdateNetworkForward(networkName, listenAddress, newData.NetworkForwardPut, "")
	}

	// Get the current config.
	forward, etag, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&forward, yaml.V2)
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
		newData := api.NetworkForward{} // We show the full info, but only send the writable fields.
		err = yaml.Load(content, &newData, yaml.WithKnownFields())
		if err == nil {
			newData.Normalise()
			err = d.UpdateNetworkForward(networkName, listenAddress, newData.Writable(), etag)
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
type cmdNetworkForwardDelete struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

var cmdNetworkForwardDeleteUsage = u.Usage{u.Network.Remote(), u.ListenAddress}

func (c *cmdNetworkForwardDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkForwardDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network forwards")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network forwards"))
	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// Delete the network forward.
	err = d.DeleteNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network forward %s deleted")+"\n", listenAddress)
	}

	return nil
}

// Add/Remove Port.
type cmdNetworkForwardPort struct {
	global          *cmdGlobal
	networkForward  *cmdNetworkForward
	flagRemoveForce bool
	flagDescription string
}

func (c *cmdNetworkForwardPort) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("port")
	cmd.Short = i18n.G("Manage network forward ports")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network forward ports"))

	// Port Add.
	cmd.AddCommand(c.commandAdd())

	// Port Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

var cmdNetworkForwardPortAddUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Protocol, u.ListenPort.List(1, ","), u.Target(u.Address), u.Target(u.Port).List(0, ",")}

func (c *cmdNetworkForwardPort) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkForwardPortAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add ports to a forward")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add ports to a forward"))
	cmd.RunE = c.runAdd

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Port description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		if len(args) == 2 {
			return []string{"tcp", "udp"}, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardPort) runAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardPortAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	listenAddress := parsed[1].String
	protocol := parsed[2].String
	// Only the list’s string representation is used.
	listenPorts := parsed[3].String
	targetAddress := parsed[4].String
	// Only the list’s string representation is used.
	targetPorts := parsed[5].String

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// Get the network forward.
	forward, etag, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	forward.Ports = append(forward.Ports, api.NetworkForwardPort{
		Protocol:      protocol,
		ListenPort:    listenPorts,
		TargetAddress: targetAddress,
		TargetPort:    targetPorts,
		Description:   c.flagDescription,
	})

	forward.Normalise()

	return d.UpdateNetworkForward(networkName, forward.ListenAddress, forward.Writable(), etag)
}

var cmdNetworkForwardPortRemoveUsage = u.Usage{u.Network.Remote(), u.ListenAddress, u.Protocol.Optional(u.ListenPort.List(0, ","))}

func (c *cmdNetworkForwardPort) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkForwardPortRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove ports from a forward")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove ports from a forward"))
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, i18n.G("Remove all ports that match"))
	cmd.RunE = c.runRemove

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkForwards(args[0])
		}

		if len(args) == 2 {
			return []string{"tcp", "udp"}, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkForwardPort) runRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkForwardPortRemoveUsage.Parse(c.global.conf, cmd, args)
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		d = d.UseTarget(c.networkForward.flagTarget)
	}

	// Get the network forward.
	forward, etag, err := d.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return err
	}

	removed := false
	newPorts := make([]api.NetworkForwardPort, 0, len(forward.Ports))

	for _, port := range forward.Ports {
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

	forward.Ports = newPorts
	forward.Normalise()

	return d.UpdateNetworkForward(networkName, forward.ListenAddress, forward.Writable(), etag)
}
