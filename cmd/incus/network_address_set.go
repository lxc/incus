package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/termios"
)

// cmdNetworkAddressSet represents the global network address set command.
type cmdNetworkAddressSet struct {
	global *cmdGlobal
}

// Command initializes the base network address set command and its subcommands.
func (c *cmdNetworkAddressSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("address-set")
	cmd.Short = i18n.G("Manage network address sets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network address sets"))

	// List
	networkAddressSetListCmd := cmdNetworkAddressSetList{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetListCmd.Command())

	// Show
	networkAddressSetShowCmd := cmdNetworkAddressSetShow{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetShowCmd.Command())

	// Create
	networkAddressSetCreateCmd := cmdNetworkAddressSetCreate{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetCreateCmd.Command())

	// Set
	networkAddressSetSetCmd := cmdNetworkAddressSetSet{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetSetCmd.Command())

	// Unset
	networkAddressSetUnsetCmd := cmdNetworkAddressSetUnset{global: c.global, networkAddressSet: c, networkAddressSetSet: &networkAddressSetSetCmd}
	cmd.AddCommand(networkAddressSetUnsetCmd.Command())

	// Edit
	networkAddressSetEditCmd := cmdNetworkAddressSetEdit{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetEditCmd.Command())

	// Rename
	networkAddressSetRenameCmd := cmdNetworkAddressSetRename{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetRenameCmd.Command())

	// Delete
	networkAddressSetDeleteCmd := cmdNetworkAddressSetDelete{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetDeleteCmd.Command())

	// Add
	networkAddressSetAddCmd := cmdNetworkAddressSetAdd{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetAddCmd.Command())

	// Remove
	networkAddressSetRemoveCmd := cmdNetworkAddressSetRemove{global: c.global, networkAddressSet: c}
	cmd.AddCommand(networkAddressSetRemoveCmd.Command())

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

// Command initializes the list subcommand.
func (c *cmdNetworkAddressSetList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network address sets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network address sets"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List address sets across all projects"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the list command logic.
func (c *cmdNetworkAddressSetList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	var sets []api.NetworkAddressSet
	if c.flagAllProjects {
		sets, err = resource.server.GetNetworkAddressSetsAllProjects()
		if err != nil {
			return err
		}
	} else {
		sets, err = resource.server.GetNetworkAddressSets()
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

// Command initializes the show subcommand.
func (c *cmdNetworkAddressSetShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<address-set>"))
	cmd.Short = i18n.G("Show network address set configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network address set configuration"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the show command logic.
func (c *cmdNetworkAddressSetShow) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	addrSet, _, err := resource.server.GetNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(addrSet.UsedBy)

	data, err := yaml.Marshal(&addrSet)
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

// Command initializes the create subcommand.
func (c *cmdNetworkAddressSetCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<address-set> [key=value...]"))
	cmd.Short = i18n.G("Create new network address sets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network address sets"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network address-set create as1

incus network address-set create as1 < config.yaml
    Create network address set with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Network address set description")+"``")
	cmd.RunE = c.Run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the create command logic.
func (c *cmdNetworkAddressSetCreate) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	var asPut api.NetworkAddressSetPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &asPut)
		if err != nil {
			return err
		}
	}

	addrSet := api.NetworkAddressSetsPost{
		NetworkAddressSetPost: api.NetworkAddressSetPost{
			Name: resource.name,
		},
		NetworkAddressSetPut: asPut,
	}

	if c.flagDescription != "" {
		addrSet.Description = c.flagDescription
	}

	if addrSet.Config == nil {
		addrSet.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		if entry[0] == "addresses" {
			addresses := strings.Split(entry[1], ",") // Split the comma-separated IPs
			addrSet.Addresses = append(addrSet.Addresses, addresses...)
			continue
		}

		addrSet.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateNetworkAddressSet(addrSet)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network address set %s created")+"\n", resource.name)
	}

	return nil
}

// cmdNetworkAddressSetSet defines the structure for setting network address set configuration.
type cmdNetworkAddressSetSet struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet

	flagIsProperty bool
}

// Command initializes the set subcommand.
func (c *cmdNetworkAddressSetSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<address-set> <key>=<value>..."))
	cmd.Short = i18n.G("Set network address set configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Set network address set configuration keys`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network address set property"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the set command logic.
func (c *cmdNetworkAddressSetSet) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	// Get current address set
	addrSet, etag, err := resource.server.GetNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	keys, err := getConfig(args[1:]...)
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
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateNetworkAddressSet(resource.name, writable, etag)
}

// cmdNetworkAddressSetUnset defines the structure for unsetting network address set configuration keys.
type cmdNetworkAddressSetUnset struct {
	global               *cmdGlobal
	networkAddressSet    *cmdNetworkAddressSet
	networkAddressSetSet *cmdNetworkAddressSetSet

	flagIsProperty bool
}

// Command initializes the unset subcommand.
func (c *cmdNetworkAddressSetUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<address-set> <key>"))
	cmd.Short = i18n.G("Unset network address set configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network address set configuration keys"))
	cmd.RunE = c.Run

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

// Run executes the unset command logic.
func (c *cmdNetworkAddressSetUnset) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkAddressSetSet.flagIsProperty = c.flagIsProperty
	args = append(args, "")
	return c.networkAddressSetSet.Run(cmd, args)
}

// cmdNetworkAddressSetEdit defines the structure for editing a network address set.
type cmdNetworkAddressSetEdit struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

// Command initializes the edit subcommand.
func (c *cmdNetworkAddressSetEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<address-set>"))
	cmd.Short = i18n.G("Edit network address set configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network address set configurations as YAML"))
	cmd.RunE = c.Run

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

// Run executes the edit command logic.
func (c *cmdNetworkAddressSetEdit) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	// If stdin isn't terminal, read yaml from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkAddressSet{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkAddressSet(resource.name, newdata.Writable(), "")
	}

	// Get current config
	addrSet, etag, err := resource.server.GetNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&addrSet)
	if err != nil {
		return err
	}

	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		newdata := api.NetworkAddressSet{}
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetworkAddressSet(resource.name, newdata.Writable(), etag)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err2 := os.Stdin.Read(make([]byte, 1))
			if err2 != nil {
				return err2
			}

			content, err2 = textEditor("", content)
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

// Command initializes the rename subcommand.
func (c *cmdNetworkAddressSetRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<address-set> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network address sets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Rename network address sets"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the rename command logic.
func (c *cmdNetworkAddressSetRename) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	err = resource.server.RenameNetworkAddressSet(resource.name, api.NetworkAddressSetPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network address set %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// cmdNetworkAddressSetDelete defines the structure for deleting a network address set.
type cmdNetworkAddressSetDelete struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

// Command initializes the delete subcommand.
func (c *cmdNetworkAddressSetDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<address-set>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network address sets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network address sets"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the delete command logic.
func (c *cmdNetworkAddressSetDelete) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	err = resource.server.DeleteNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network address set %s deleted")+"\n", resource.name)
	}

	return nil
}

// cmdNetworkAddressSetAdd defines the structure for adding addresses to a network address set.
type cmdNetworkAddressSetAdd struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

// Command initializes the add subcommand.
func (c *cmdNetworkAddressSetAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<address-set> <address>..."))
	cmd.Short = i18n.G("Add addresses to a network address set")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add addresses to a network address set"))

	cmd.RunE = c.Run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the add command logic.
func (c *cmdNetworkAddressSetAdd) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	addrSet, etag, err := resource.server.GetNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	// Add addresses
	addrSet.Addresses = append(addrSet.Addresses, args[1:]...)

	return resource.server.UpdateNetworkAddressSet(resource.name, addrSet.Writable(), etag)
}

// cmdNetworkAddressSetRemove defines the structure for removing addresses from a network address set.
type cmdNetworkAddressSetRemove struct {
	global            *cmdGlobal
	networkAddressSet *cmdNetworkAddressSet
}

// Command initializes the remove subcommand.
func (c *cmdNetworkAddressSetRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<address-set> <address>..."))
	cmd.Short = i18n.G("Remove addresses from a network address set")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove addresses from a network address set"))

	cmd.RunE = c.Run
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkAddressSets(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the del-addr command logic.
func (c *cmdNetworkAddressSetRemove) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing network address set name"))
	}

	addrSet, etag, err := resource.server.GetNetworkAddressSet(resource.name)
	if err != nil {
		return err
	}

	toRemove := args[1:]
	newAddrs := make([]string, 0, len(addrSet.Addresses))
	removedCount := 0

	for _, addr := range addrSet.Addresses {
		match := false

		for _, r := range toRemove {
			if r == addr {
				match = true
				removedCount++
				break
			}
		}

		if !match {
			newAddrs = append(newAddrs, addr)
		}
	}

	if removedCount != len(toRemove) {
		return errors.New(i18n.G("One or more provided address isn't currently in the set"))
	}

	addrSet.Addresses = newAddrs
	return resource.server.UpdateNetworkAddressSet(resource.name, addrSet.Writable(), etag)
}
