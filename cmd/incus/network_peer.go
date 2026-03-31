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
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdNetworkPeer struct {
	global *cmdGlobal
}

func (c *cmdNetworkPeer) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("peer")
	cmd.Short = i18n.G("Manage network peerings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network peerings"))

	// List.
	networkPeerListCmd := cmdNetworkPeerList{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerListCmd.command())

	// Show.
	networkPeerShowCmd := cmdNetworkPeerShow{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerShowCmd.command())

	// Create.
	networkPeerCreateCmd := cmdNetworkPeerCreate{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerCreateCmd.command())

	// Get,
	networkPeerGetCmd := cmdNetworkPeerGet{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerGetCmd.command())

	// Set.
	networkPeerSetCmd := cmdNetworkPeerSet{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerSetCmd.command())

	// Unset.
	networkPeerUnsetCmd := cmdNetworkPeerUnset{global: c.global, networkPeer: c, networkPeerSet: &networkPeerSetCmd}
	cmd.AddCommand(networkPeerUnsetCmd.command())

	// Edit.
	networkPeerEditCmd := cmdNetworkPeerEdit{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerEditCmd.command())

	// Delete.
	networkPeerDeleteCmd := cmdNetworkPeerDelete{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerDeleteCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkPeerList struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagFormat  string
	flagColumns string
}

type networkPeerColumn struct {
	Name string
	Data func(api.NetworkPeer) string
}

var cmdNetworkPeerListUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkPeerList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkPeerListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network peers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available network peers

Default column layout: ndpts

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  d - description
  p - Peer
  t - Type
  s - State`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkPeerListColumns, i18n.G("Columns")+"``")

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

const defaultNetworkPeerListColumns = "ndpts"

func (c *cmdNetworkPeerList) parseColumns() ([]networkPeerColumn, error) {
	columnsShorthandMap := map[rune]networkPeerColumn{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'p': {i18n.G("PEER"), c.peerColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		's': {i18n.G("STATE"), c.stateColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkPeerColumn{}

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

func (c *cmdNetworkPeerList) nameColumnData(peer api.NetworkPeer) string {
	return peer.Name
}

func (c *cmdNetworkPeerList) descriptionColumnData(peer api.NetworkPeer) string {
	return peer.Description
}

func (c *cmdNetworkPeerList) peerColumnData(peer api.NetworkPeer) string {
	target := "Unknown"

	if peer.TargetProject != "" && peer.TargetNetwork != "" {
		target = fmt.Sprintf("%s/%s", peer.TargetProject, peer.TargetNetwork)
	} else if peer.TargetIntegration != "" {
		target = peer.TargetIntegration
	}

	return target
}

func (c *cmdNetworkPeerList) typeColumnData(peer api.NetworkPeer) string {
	return peer.Type
}

func (c *cmdNetworkPeerList) stateColumnData(peer api.NetworkPeer) string {
	return strings.ToUpper(peer.Status)
}

func (c *cmdNetworkPeerList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	peers, err := d.GetNetworkPeers(networkName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, peer := range peers {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(peer))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, peers)
}

// Show.
type cmdNetworkPeerShow struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

var cmdNetworkPeerShowUsage = u.Usage{u.Network.Remote(), u.Peer}

func (c *cmdNetworkPeerShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkPeerShowUsage...)
	cmd.Short = i18n.G("Show network peer configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network peer configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String

	// Show the network peer config.
	peer, _, err := d.GetNetworkPeer(networkName, peerName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&peer, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkPeerCreate struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagType        string
	flagDescription string
}

var cmdNetworkPeerCreateUsage = u.Usage{u.Network.Remote(), u.NewName(u.Peer), u.MakePath(u.Target(u.Project).Optional(), u.Target(u.Placeholder(i18n.G("network or integration")))), u.KV.List(0)}

func (c *cmdNetworkPeerCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkPeerCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network peering")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network peering"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network peer create default peer1 web/default
    Create a new peering between network "default" in the current project and network "default" in the "web" project

incus network peer create default peer2 ovn-ic --type=remote
    Create a new peering between network "default" in the current project and other remote networks through the "ovn-ic" integration

incus network peer create default peer3 web/default < config.yaml
	Create a new peering between network default in the current project and network default in the web project using the configuration
	in the file config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagType, "type", "local", i18n.G("Type of peer (local or remote)")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Peer description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String
	targetProject := parsed[2].List[0].String
	target := parsed[2].List[1].String
	keys, err := kvToMap(parsed[3])
	if err != nil {
		return err
	}

	if !slices.Contains([]string{"local", "remote"}, c.flagType) {
		return errors.New(i18n.G("Invalid peer type"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var peerPut api.NetworkPeerPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Load(contents, &peerPut, yaml.WithKnownFields())
		if err != nil {
			return err
		}
	}

	if peerPut.Config == nil {
		peerPut.Config = map[string]string{}
	}

	maps.Copy(peerPut.Config, keys)

	// Create the network peer.
	peer := api.NetworkPeersPost{
		Name:           peerName,
		NetworkPeerPut: peerPut,
		Type:           c.flagType,
	}

	switch c.flagType {
	case "local":
		peer.TargetProject = targetProject
		peer.TargetNetwork = target
	case "remote":
		peer.TargetIntegration = target
	}

	if c.flagDescription != "" {
		peer.Description = c.flagDescription
	}

	err = d.CreateNetworkPeer(networkName, peer)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		createdPeer, _, err := d.GetNetworkPeer(networkName, peer.Name)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed getting peer's status: %w"), err)
		}

		switch createdPeer.Status {
		case api.NetworkStatusCreated:
			fmt.Printf(i18n.G("Network peer %s created")+"\n", peer.Name)
		case api.NetworkStatusPending:
			fmt.Printf(i18n.G("Network peer %s pending (please complete mutual peering on peer network)")+"\n", peer.Name)
		default:
			fmt.Printf(i18n.G("Network peer %s is in unexpected state %q")+"\n", peer.Name, createdPeer.Status)
		}
	}

	return nil
}

// Get.
type cmdNetworkPeerGet struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagIsProperty bool
}

var cmdNetworkPeerGetUsage = u.Usage{u.Network.Remote(), u.Peer, u.Key}

func (c *cmdNetworkPeerGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkPeerGetUsage...)
	cmd.Short = i18n.G("Get values for network peer configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network peer configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network peer property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkPeerConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String
	key := parsed[1].String

	// Get the current config.
	peer, _, err := d.GetNetworkPeer(networkName, peerName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := peer.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network peer %q: %v"), key, peerName, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range peer.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkPeerSet struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagIsProperty bool
}

var cmdNetworkPeerSetUsage = u.Usage{u.Network.Remote(), u.Peer, u.LegacyKV.List(1)}

func (c *cmdNetworkPeerSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkPeerSetUsage...)
	cmd.Short = i18n.G("Set network peer keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network peer keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <peer_name> <key> <value>`))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network peer property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkPeerSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// Get the current config.
	peer, etag, err := d.GetNetworkPeer(networkName, peerName)
	if err != nil {
		return err
	}

	if peer.Config == nil {
		peer.Config = map[string]string{}
	}

	writable := peer.Writable()
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

	return d.UpdateNetworkPeer(networkName, peer.Name, writable, etag)
}

func (c *cmdNetworkPeerSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkPeerUnset struct {
	global         *cmdGlobal
	networkPeer    *cmdNetworkPeer
	networkPeerSet *cmdNetworkPeerSet

	flagIsProperty bool
}

var cmdNetworkPeerUnsetUsage = u.Usage{u.Network.Remote(), u.Peer, u.Key}

func (c *cmdNetworkPeerUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkPeerUnsetUsage...)
	cmd.Short = i18n.G("Unset network peer configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network peer keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network peer property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkPeerConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkPeerSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkPeerSet, cmd, parsed)
}

// Edit.
type cmdNetworkPeerEdit struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

var cmdNetworkPeerEditUsage = u.Usage{u.Network.Remote(), u.Peer}

func (c *cmdNetworkPeerEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkPeerEditUsage...)
	cmd.Short = i18n.G("Edit network peer configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network peer configurations as YAML"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network peer.
### Any line starting with a '# will be ignored.
###
### An example would look like:
### description: A peering to mynet
### config: {}
### name: mypeer
### target_project: default
### target_network: mynet
### status: Pending
###
### Note that the name, target_project, target_network and status fields cannot be changed.`)
}

func (c *cmdNetworkPeerEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus network peer show` command to be passed in here, but only take the contents
		// of the NetworkPeerPut fields when updating. The other fields are silently discarded.
		newData := api.NetworkPeer{}
		err = yaml.Load(contents, &newData, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		return d.UpdateNetworkPeer(networkName, peerName, newData.NetworkPeerPut, "")
	}

	// Get the current config.
	peer, etag, err := d.GetNetworkPeer(networkName, peerName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&peer, yaml.V2)
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
		newData := api.NetworkPeer{} // We show the full info, but only send the writable fields.
		err = yaml.Load(content, &newData, yaml.WithKnownFields())
		if err == nil {
			err = d.UpdateNetworkPeer(networkName, peerName, newData.Writable(), etag)
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
type cmdNetworkPeerDelete struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

var cmdNetworkPeerDeleteUsage = u.Usage{u.Network.Remote(), u.Peer}

func (c *cmdNetworkPeerDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkPeerDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network peerings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network peerings"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkPeerDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	peerName := parsed[1].String

	// Delete the network peer.
	err = d.DeleteNetworkPeer(networkName, peerName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network peer %s deleted")+"\n", peerName)
	}

	return nil
}
