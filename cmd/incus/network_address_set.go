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

// cmdNetworkAddressSet represents the global network address set command.
type cmdNetworkAddressSet struct {
	global *cmdGlobal
}

func (c *cmdNetworkAddressSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("address-set")
	cmd.Short = i18n.G("Manage network address sets")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network address sets"))

	// List
	networkAddressSetListCmd := cmdNetworkAddressSetList{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetListCmd.command())

	// Show
	networkAddressSetShowCmd := cmdNetworkAddressSetShow{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetShowCmd.command())

	// Create
	networkAddressSetCreateCmd := cmdNetworkAddressSetCreate{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetCreateCmd.command())

	// Set
	networkAddressSetSetCmd := cmdNetworkAddressSetSet{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetSetCmd.command())

	// Unset
	networkAddressSetUnsetCmd := cmdNetworkAddressSetUnset{global: c.global, networkAddressSet: c, networkAddressSetSet: &networkAddressSetSetCmd}
	cmd.AddCommand(networkAddressSetUnsetCmd.command())

	// Edit
	networkAddressSetEditCmd := cmdNetworkAddressSetEdit{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetEditCmd.command())

	// Rename
	networkAddressSetRenameCmd := cmdNetworkAddressSetRename{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetRenameCmd.command())

	// Delete
	networkAddressSetDeleteCmd := cmdNetworkAddressSetDelete{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetDeleteCmd.command())

	// Add
	networkAddressSetAddCmd := cmdNetworkAddressSetAdd{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetAddCmd.command())

	// Remove
	networkAddressSetRemoveCmd := cmdNetworkAddressSetRemove{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetRemoveCmd.command())

	// Workaround for subcommand usage errors
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// cmdNetworkAddressSetList defines the structure for listing network address sets.
type cmdNetworkAddressSetList struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet

	flagFormat      string
	flagAllProjects bool
}

var cmdNetworkAddressSetListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkAddressSetList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkAddressSetListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network address sets")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List available network address sets"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G("Format (csv|json|table|yaml|compact|markdown)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List address sets across all projects"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	var sets []api.NetworkAddressSet
	if c.flagAllProjects {
		sets, err = d.GetNetworkAddressSetsAllProjects()
		if err != nil {
			return err
		}
	} else {
		sets, err = d.GetNetworkAddressSets()
		if err != nil {
			return err
		}
	}
	data := [][]string{}
	for _, as := range sets {
		strUsedBy := fmt.Sprintf("%d", len(as.UsedBy))
		details := []string{
			as.Name,
			as.Description,
			strings.Join(as.Addresses, "\n"),
			strUsedBy,
		}

		if c.flagAllProjects {
			details = append([]string{as.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("ADDRESSES"),
		i18n.G("USED BY"),
	}

	if c.flagAllProjects {
		header = append([]string{i18n.G("PROJECT")}, header...)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, sets)
}

// cmdNetworkAddressSetShow defines the structure for showing a network address set.
type cmdNetworkAddressSetShow struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetShowUsage = u.Usage{u.AddressSet.Remote()}

func (c *cmdNetworkAddressSetShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkAddressSetShowUsage...)
	cmd.Short = i18n.G("Show network address set configuration")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network address set configuration"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String

	addrSet, _, err := d.GetNetworkAddressSet(addressSetName)
	if err != nil {
		return err
	}

	sort.Strings(addrSet.UsedBy)

	data, err := yaml.Dump(&addrSet, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)
	return nil
}

// cmdNetworkAddressSetCreate defines the structure for creating a network address set.
type cmdNetworkAddressSetCreate struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet

	flagDescription string
}

var cmdNetworkAddressSetCreateUsage = u.Usage{u.NewName(u.AddressSet).Remote(), u.KV.List(0)}

func (c *cmdNetworkAddressSetCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkAddressSetCreateUsage...)
	cmd.Short = i18n.G("Create new network address sets")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network address sets"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network address-set create as1

incus network address-set create as1 < config.yaml
    Create network address set with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Network address set description")+"``")
	cmd.RunE = c.run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	var asPut api.NetworkAddressSetPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		err = loader.Load(&asPut)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	addrSet := api.NetworkAddressSetsPost{
		NetworkAddressSetPost: api.NetworkAddressSetPost{
			Name: addressSetName,
		},
		NetworkAddressSetPut: asPut,
	}

	if c.flagDescription != "" {
		addrSet.Description = c.flagDescription
	}

	if addrSet.Config == nil {
		addrSet.Config = map[string]string{}
	}

	for k, v := range keys {
		if k == "addresses" {
			addresses := strings.Split(v, ",") // Split the comma-separated IPs
			addrSet.Addresses = append(addrSet.Addresses, addresses...)
			continue
		}

		addrSet.Config[k] = v
	}

	err = d.CreateNetworkAddressSet(addrSet)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network address set %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// cmdNetworkAddressSetSet defines the structure for setting network address set configuration.
type cmdNetworkAddressSetSet struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet

	flagIsProperty bool
}

var cmdNetworkAddressSetSetUsage = u.Usage{u.AddressSet.Remote(), u.LegacyKV.List(1)}

func (c *cmdNetworkAddressSetSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkAddressSetSetUsage...)
	cmd.Short = i18n.G("Set network address set configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Set network address set configuration keys`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network address set property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkAddressSetSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get current address set
	addrSet, etag, err := d.GetNetworkAddressSet(addressSetName)
	if err != nil {
		return err
	}

	writable := addrSet.Writable()
	if writable.Config == nil {
		writable.Config = make(map[string]string)
	}

	if c.flagIsProperty {
		// handle as properties
		err = unpackKVToWritable(&writable, keys)
		if err != nil {
			return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return d.UpdateNetworkAddressSet(addressSetName, writable, etag)
}

func (c *cmdNetworkAddressSetSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// cmdNetworkAddressSetUnset defines the structure for unsetting network address set configuration keys.
type cmdNetworkAddressSetUnset struct {
	global               *cmdGlobal
	networkAddressSet    *cmdNetworkAddressSet
	networkAddressSetSet *cmdNetworkAddressSetSet

	flagIsProperty bool
}

var cmdNetworkAddressSetUnsetUsage = u.Usage{u.AddressSet.Remote(), u.Key}

func (c *cmdNetworkAddressSetUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkAddressSetUnsetUsage...)
	cmd.Short = i18n.G("Unset network address set configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network address set configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network address set property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkAddressSetConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkAddressSetSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkAddressSetSet, cmd, parsed)
}

// cmdNetworkAddressSetEdit defines the structure for editing a network address set.
type cmdNetworkAddressSetEdit struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetEditUsage = u.Usage{u.AddressSet.Remote()}

func (c *cmdNetworkAddressSetEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkAddressSetEditUsage...)
	cmd.Short = i18n.G("Edit network address set configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network address set configurations as YAML"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// helpTemplate provides a YAML template for editing address sets.
func (c *cmdNetworkAddressSetEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network address set.
### Any line starting with '#' will be ignored.
###
### For example:
### name: as1
### description: "Test address set"
### addresses:
###  - 10.0.0.1
###  - 2001:db8::1
### external_ids:
###  user.foo: bar
`)
}

func (c *cmdNetworkAddressSetEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String

	// If stdin isn't terminal, read yaml from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		newdata := api.NetworkAddressSet{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateNetworkAddressSet(addressSetName, newdata.Writable(), "")
	}

	// Get current config
	addrSet, etag, err := d.GetNetworkAddressSet(addressSetName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&addrSet, yaml.V2)
	if err != nil {
		return err
	}

	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		newdata := api.NetworkAddressSet{}
		err = yaml.Load(content, &newdata, yaml.WithKnownFields())
		if err == nil {
			err = d.UpdateNetworkAddressSet(addressSetName, newdata.Writable(), etag)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err2 := os.Stdin.Read(make([]byte, 1))
			if err2 != nil {
				return err2
			}

			content, err2 = cli.TextEditor("", content)
			if err2 != nil {
				return err2
			}

			continue
		}

		break
	}

	return nil
}

// cmdNetworkAddressSetRename defines the structure for renaming a network address set.
type cmdNetworkAddressSetRename struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetRenameUsage = u.Usage{u.AddressSet.Remote(), u.NewName(u.AddressSet)}

func (c *cmdNetworkAddressSetRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdNetworkAddressSetRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network address sets")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Rename network address sets"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String
	newAddressSetName := parsed[1].String

	err = d.RenameNetworkAddressSet(addressSetName, api.NetworkAddressSetPost{Name: newAddressSetName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network address set %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newAddressSetName)
	}

	return nil
}

// cmdNetworkAddressSetDelete defines the structure for deleting a network address set.
type cmdNetworkAddressSetDelete struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetDeleteUsage = u.Usage{u.AddressSet.Remote().List(1)}

func (c *cmdNetworkAddressSetDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkAddressSetDeleteUsage...)
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network address sets")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network address sets"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpNetworkAddressSets(toComplete)
	}

	return cmd
}

func (c *cmdNetworkAddressSetDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		addressSetName := p.RemoteObject.String

		// Delete the address set.
		err = d.DeleteNetworkAddressSet(addressSetName)
		if err != nil {
			return err
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network address set %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	return nil
}

// cmdNetworkAddressSetAdd defines the structure for adding addresses to a network address set.
type cmdNetworkAddressSetAdd struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetAddUsage = u.Usage{u.AddressSet.Remote(), u.Address.List(1)}

func (c *cmdNetworkAddressSetAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkAddressSetAddUsage...)
	cmd.Short = i18n.G("Add addresses to a network address set")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add addresses to a network address set"))

	cmd.RunE = c.run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String
	addresses := parsed[1].StringList

	addrSet, etag, err := d.GetNetworkAddressSet(addressSetName)
	if err != nil {
		return err
	}

	// Add addresses
	addrSet.Addresses = append(addrSet.Addresses, addresses...)

	return d.UpdateNetworkAddressSet(addressSetName, addrSet.Writable(), etag)
}

// cmdNetworkAddressSetRemove defines the structure for removing addresses from a network address set.
type cmdNetworkAddressSetRemove struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

var cmdNetworkAddressSetRemoveUsage = u.Usage{u.AddressSet.Remote(), u.Address.List(1)}

func (c *cmdNetworkAddressSetRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkAddressSetRemoveUsage...)
	cmd.Short = i18n.G("Remove addresses from a network address set")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove addresses from a network address set"))

	cmd.RunE = c.run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAddressSetRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAddressSetRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	addressSetName := parsed[0].RemoteObject.String
	addresses := parsed[1].StringList

	addrSet, etag, err := d.GetNetworkAddressSet(addressSetName)
	if err != nil {
		return err
	}

	newAddrs := make([]string, 0, len(addrSet.Addresses))
	removedCount := 0

	for _, addr := range addrSet.Addresses {
		match := false

		if slices.Contains(addresses, addr) {
			match = true
			removedCount++
		}

		if !match {
			newAddrs = append(newAddrs, addr)
		}
	}

	if removedCount != len(addresses) {
		return errors.New(i18n.G("One or more provided address isn't currently in the set"))
	}

	addrSet.Addresses = newAddrs
	return d.UpdateNetworkAddressSet(addressSetName, addrSet.Writable(), etag)
}
