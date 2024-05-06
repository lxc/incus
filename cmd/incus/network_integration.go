package main

import (
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

type cmdNetworkIntegration struct {
	global *cmdGlobal
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegration) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("integration")
	cmd.Short = i18n.G("Manage network integrations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage network integrations`))

	// Create
	networkIntegrationCreateCmd := cmdNetworkIntegrationCreate{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationCreateCmd.Command())

	// Delete
	networkIntegrationDeleteCmd := cmdNetworkIntegrationDelete{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationDeleteCmd.Command())

	// Edit
	networkIntegrationEditCmd := cmdNetworkIntegrationEdit{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationEditCmd.Command())

	// Get
	networkIntegrationGetCmd := cmdNetworkIntegrationGet{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationGetCmd.Command())

	// List
	networkIntegrationListCmd := cmdNetworkIntegrationList{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationListCmd.Command())

	// Rename
	networkIntegrationRenameCmd := cmdNetworkIntegrationRename{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationRenameCmd.Command())

	// Set
	networkIntegrationSetCmd := cmdNetworkIntegrationSet{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationSetCmd.Command())

	// Unset
	networkIntegrationUnsetCmd := cmdNetworkIntegrationUnset{global: c.global, networkIntegration: c, networkIntegrationSet: &networkIntegrationSetCmd}
	cmd.AddCommand(networkIntegrationUnsetCmd.Command())

	// Show
	networkIntegrationShowCmd := cmdNetworkIntegrationShow{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdNetworkIntegrationCreate struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
	flagConfig         []string
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<network integration> <type>"))
	cmd.Short = i18n.G("Create network integrations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create network integrations`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network integration create o1 ovn

incus network integration create o1 ovn < config.yaml
    Create network integration o1 of type ovn with configuration from config.yaml`))

	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new network integration")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationCreate) Run(cmd *cobra.Command, args []string) error {
	var stdinData api.NetworkIntegrationPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Create the network integration
	networkIntegration := api.NetworkIntegrationsPost{}
	networkIntegration.Name = resource.name
	networkIntegration.Type = args[1]
	networkIntegration.Description = stdinData.Description

	if stdinData.Config == nil {
		networkIntegration.Config = map[string]string{}
		for _, entry := range c.flagConfig {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
			}

			networkIntegration.Config[key] = value
		}
	} else {
		networkIntegration.Config = stdinData.Config
	}

	err = resource.server.CreateNetworkIntegration(networkIntegration)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network integration %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdNetworkIntegrationDelete struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<network integration>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network integrations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete network integrations`))

	cmd.RunE = c.Run

	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Get the network integration.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Delete the network integration
	err = resource.server.DeleteNetworkIntegration(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network integration %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdNetworkIntegrationEdit struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<network integration>"))
	cmd.Short = i18n.G("Edit network integration configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit network integration configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network integration edit <network integration> < network-integration.yaml
    Update a network integration using the content of network-integration.yaml`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkIntegrationEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network integration.
### Any line starting with a '# will be ignored.
###
### Note that the name is shown but cannot be changed`)
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkIntegrationPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkIntegration(resource.name, newdata, "")
	}

	// Extract the current value
	networkIntegration, etag, err := resource.server.GetNetworkIntegration(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&networkIntegration)
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
		newdata := api.NetworkIntegrationPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetworkIntegration(resource.name, newdata, etag)
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

// Get.
type cmdNetworkIntegrationGet struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagIsProperty bool
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<network integration> <key>"))
	cmd.Short = i18n.G("Get values for network integration configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for network integration configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network integration property"))
	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Get the configuration key
	networkIntegration, _, err := resource.server.GetNetworkIntegration(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := networkIntegration.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network integration %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", networkIntegration.Config[args[1]])
	}

	return nil
}

// List.
type cmdNetworkIntegrationList struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagFormat string
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List network integrations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List network integrations`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := conf.DefaultRemote
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List network integrations
	networkIntegrations, err := resource.server.GetNetworkIntegrations()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, networkIntegration := range networkIntegrations {
		strUsedBy := fmt.Sprintf("%d", len(networkIntegration.UsedBy))
		data = append(data, []string{networkIntegration.Name, networkIntegration.Description, networkIntegration.Type, strUsedBy})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("TYPE"),
		i18n.G("USED BY"),
	}

	return cli.RenderTable(c.flagFormat, header, data, networkIntegrations)
}

// Rename.
type cmdNetworkIntegrationRename struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<network integration> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network integrations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename network integrations`))

	cmd.RunE = c.Run

	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationRename) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Rename the network integration
	err = resource.server.RenameNetworkIntegration(resource.name, api.NetworkIntegrationPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network integration %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Set.
type cmdNetworkIntegrationSet struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagIsProperty bool
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<network integration> <key>=<value>..."))
	cmd.Short = i18n.G("Set network integration configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network integration configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus network integration set [<remote>:]<network integration> <key> <value>`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network integration property"))
	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationSet) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Get the network integration
	networkIntegration, etag, err := resource.server.GetNetworkIntegration(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := networkIntegration.Writable()
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

	return resource.server.UpdateNetworkIntegration(resource.name, writable, etag)
}

// Unset.
type cmdNetworkIntegrationUnset struct {
	global                *cmdGlobal
	networkIntegration    *cmdNetworkIntegration
	networkIntegrationSet *cmdNetworkIntegrationSet

	flagIsProperty bool
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<network integration> <key>"))
	cmd.Short = i18n.G("Unset network integration configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset network integration configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network integration property"))
	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkIntegrationSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkIntegrationSet.Run(cmd, args)
}

// Show.
type cmdNetworkIntegrationShow struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

// Command returns a cobra command for inclusion.
func (c *cmdNetworkIntegrationShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<network integration>"))
	cmd.Short = i18n.G("Show network integration options")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show network integration options`))

	cmd.RunE = c.Run

	return cmd
}

// Run actually performs the action.
func (c *cmdNetworkIntegrationShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network integration name"))
	}

	// Show the network integration
	networkIntegration, _, err := resource.server.GetNetworkIntegration(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&networkIntegration)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
