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

type cmdNetworkIntegration struct {
	global *cmdGlobal
}

func (c *cmdNetworkIntegration) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("integration")
	cmd.Short = i18n.G("Manage network integrations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage network integrations`))

	// Create
	networkIntegrationCreateCmd := cmdNetworkIntegrationCreate{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationCreateCmd.command())

	// Delete
	networkIntegrationDeleteCmd := cmdNetworkIntegrationDelete{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationDeleteCmd.command())

	// Edit
	networkIntegrationEditCmd := cmdNetworkIntegrationEdit{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationEditCmd.command())

	// Get
	networkIntegrationGetCmd := cmdNetworkIntegrationGet{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationGetCmd.command())

	// List
	networkIntegrationListCmd := cmdNetworkIntegrationList{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationListCmd.command())

	// Rename
	networkIntegrationRenameCmd := cmdNetworkIntegrationRename{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationRenameCmd.command())

	// Set
	networkIntegrationSetCmd := cmdNetworkIntegrationSet{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationSetCmd.command())

	// Unset
	networkIntegrationUnsetCmd := cmdNetworkIntegrationUnset{global: c.global, networkIntegration: c, networkIntegrationSet: &networkIntegrationSetCmd}
	cmd.AddCommand(networkIntegrationUnsetCmd.command())

	// Show
	networkIntegrationShowCmd := cmdNetworkIntegrationShow{global: c.global, networkIntegration: c}
	cmd.AddCommand(networkIntegrationShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdNetworkIntegrationCreate struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
	flagConfig         []string
}

var cmdNetworkIntegrationCreateUsage = u.Usage{u.NewName(u.NetworkIntegration).Remote(), u.Type}

func (c *cmdNetworkIntegrationCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkIntegrationCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create network integrations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create network integrations`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network integration create o1 ovn

incus network integration create o1 ovn < config.yaml
    Create network integration o1 of type ovn with configuration from config.yaml`))

	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new network integration")+"``")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkIntegrationCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String
	integrationType := parsed[1].String

	var stdinData api.NetworkIntegrationPut

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Load(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Create the network integration
	networkIntegration := api.NetworkIntegrationsPost{}
	networkIntegration.Name = integrationName
	networkIntegration.Type = integrationType
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

	err = d.CreateNetworkIntegration(networkIntegration)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network integration %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Delete.
type cmdNetworkIntegrationDelete struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

var cmdNetworkIntegrationDeleteUsage = u.Usage{u.NetworkIntegration.Remote().List(1)}

func (c *cmdNetworkIntegrationDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkIntegrationDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network integrations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete network integrations`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkIntegrationDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		integrationName := p.RemoteObject.String

		// Delete the network integration
		err = d.DeleteNetworkIntegration(integrationName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network integration %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdNetworkIntegrationEdit struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

var cmdNetworkIntegrationEditUsage = u.Usage{u.NetworkIntegration.Remote()}

func (c *cmdNetworkIntegrationEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkIntegrationEditUsage...)
	cmd.Short = i18n.G("Edit network integration configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit network integration configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus network integration edit <network integration> < network-integration.yaml
    Update a network integration using the content of network-integration.yaml`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkIntegrationEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network integration.
### Any line starting with a '# will be ignored.
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdNetworkIntegrationEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkIntegrationPut{}
		err = yaml.Load(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateNetworkIntegration(integrationName, newdata, "")
	}

	// Extract the current value
	networkIntegration, etag, err := d.GetNetworkIntegration(integrationName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&networkIntegration, yaml.V2)
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
		newdata := api.NetworkIntegrationPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateNetworkIntegration(integrationName, newdata, etag)
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

// Get.
type cmdNetworkIntegrationGet struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagIsProperty bool
}

type networkIntegrationColumn struct {
	Name string
	Data func(api.NetworkIntegration) string
}

var cmdNetworkIntegrationGetUsage = u.Usage{u.NetworkIntegration.Remote(), u.Key}

func (c *cmdNetworkIntegrationGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkIntegrationGetUsage...)
	cmd.Short = i18n.G("Get values for network integration configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for network integration configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network integration property"))
	return cmd
}

func (c *cmdNetworkIntegrationGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the configuration key
	networkIntegration, _, err := d.GetNetworkIntegration(integrationName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := networkIntegration.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network integration %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", networkIntegration.Config[key])
	}

	return nil
}

// List.
type cmdNetworkIntegrationList struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagFormat  string
	flagColumns string
}

var cmdNetworkIntegrationListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkIntegrationList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkIntegrationListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List network integrations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List network integrations

Default column layout: ndtu

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
	n - Name
	d - Description
	t - Type
	u - Used by`))

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkIntegrationColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	return cmd
}

const defaultNetworkIntegrationColumns = "ndtu"

func (c *cmdNetworkIntegrationList) parseColumns() ([]networkIntegrationColumn, error) {
	columnsShorthandMap := map[rune]networkIntegrationColumn{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkIntegrationColumn{}

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

func (c *cmdNetworkIntegrationList) nameColumnData(integration api.NetworkIntegration) string {
	return integration.Name
}

func (c *cmdNetworkIntegrationList) descriptionColumnData(integration api.NetworkIntegration) string {
	return integration.Description
}

func (c *cmdNetworkIntegrationList) typeColumnData(integration api.NetworkIntegration) string {
	return integration.Type
}

func (c *cmdNetworkIntegrationList) usedByColumnData(integration api.NetworkIntegration) string {
	return fmt.Sprintf("%d", len(integration.UsedBy))
}

func (c *cmdNetworkIntegrationList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// List network integrations
	networkIntegrations, err := d.GetNetworkIntegrations()
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, networkIntegration := range networkIntegrations {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(networkIntegration))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, networkIntegrations)
}

// Rename.
type cmdNetworkIntegrationRename struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

var cmdNetworkIntegrationRenameUsage = u.Usage{u.NetworkIntegration.Remote(), u.NewName(u.NetworkIntegration)}

func (c *cmdNetworkIntegrationRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdNetworkIntegrationRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network integrations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename network integrations`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkIntegrationRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String
	newIntegrationName := parsed[1].String

	// Rename the network integration
	err = d.RenameNetworkIntegration(integrationName, api.NetworkIntegrationPost{Name: newIntegrationName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network integration %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newIntegrationName)
	}

	return nil
}

// Set.
type cmdNetworkIntegrationSet struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration

	flagIsProperty bool
}

var cmdNetworkIntegrationSetUsage = u.Usage{u.NetworkIntegration.Remote(), u.LegacyKV.List(1)}

func (c *cmdNetworkIntegrationSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkIntegrationSetUsage...)
	cmd.Short = i18n.G("Set network integration configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network integration configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus network integration set [<remote>:]<network integration> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network integration property"))
	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkIntegrationSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get the network integration
	networkIntegration, etag, err := d.GetNetworkIntegration(integrationName)
	if err != nil {
		return err
	}

	writable := networkIntegration.Writable()
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

	return d.UpdateNetworkIntegration(integrationName, writable, etag)
}

func (c *cmdNetworkIntegrationSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkIntegrationUnset struct {
	global                *cmdGlobal
	networkIntegration    *cmdNetworkIntegration
	networkIntegrationSet *cmdNetworkIntegrationSet

	flagIsProperty bool
}

var cmdNetworkIntegrationUnsetUsage = u.Usage{u.NetworkIntegration.Remote(), u.Key}

func (c *cmdNetworkIntegrationUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkIntegrationUnsetUsage...)
	cmd.Short = i18n.G("Unset network integration configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Unset network integration configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network integration property"))
	return cmd
}

func (c *cmdNetworkIntegrationUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkIntegrationSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkIntegrationSet, cmd, parsed)
}

// Show.
type cmdNetworkIntegrationShow struct {
	global             *cmdGlobal
	networkIntegration *cmdNetworkIntegration
}

var cmdNetworkIntegrationShowUsage = u.Usage{u.NetworkIntegration.Remote()}

func (c *cmdNetworkIntegrationShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkIntegrationShowUsage...)
	cmd.Short = i18n.G("Show network integration options")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show network integration options`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdNetworkIntegrationShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkIntegrationShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	integrationName := parsed[0].RemoteObject.String

	// Show the network integration
	networkIntegration, _, err := d.GetNetworkIntegration(integrationName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&networkIntegration, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
