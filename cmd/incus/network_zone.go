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
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdNetworkZone struct {
	global *cmdGlobal
}

type networkZoneColumn struct {
	Name string
	Data func(api.NetworkZone) string
}

func (c *cmdNetworkZone) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("zone")
	cmd.Short = i18n.G("Manage network zones")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network zones"))

	// List.
	networkZoneListCmd := cmdNetworkZoneList{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneListCmd.command())

	// Show.
	networkZoneShowCmd := cmdNetworkZoneShow{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneShowCmd.command())

	// Get.
	networkZoneGetCmd := cmdNetworkZoneGet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneGetCmd.command())

	// Create.
	networkZoneCreateCmd := cmdNetworkZoneCreate{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneCreateCmd.command())

	// Set.
	networkZoneSetCmd := cmdNetworkZoneSet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneSetCmd.command())

	// Unset.
	networkZoneUnsetCmd := cmdNetworkZoneUnset{global: c.global, networkZone: c, networkZoneSet: &networkZoneSetCmd}
	cmd.AddCommand(networkZoneUnsetCmd.command())

	// Edit.
	networkZoneEditCmd := cmdNetworkZoneEdit{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneEditCmd.command())

	// Delete.
	networkZoneDeleteCmd := cmdNetworkZoneDelete{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneDeleteCmd.command())

	// Record.
	networkZoneRecordCmd := cmdNetworkZoneRecord{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneRecordCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneList struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagFormat      string
	flagAllProjects bool
	flagColumns     string
}

var cmdNetworkZoneListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkZoneList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkZoneListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zones")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available network zone

Default column layout: nDSdus

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  d - Description
  e - Project name
  n - Name
  u - Used by`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display network zones from all projects"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkZoneColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultNetworkZoneColumns = "ndu"

func (c *cmdNetworkZoneList) parseColumns() ([]networkZoneColumn, error) {
	columnsShorthandMap := map[rune]networkZoneColumn{
		'e': {i18n.G("PROJECT"), c.projectColumnData},
		'n': {i18n.G("NAME"), c.networkZoneNameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	if c.flagColumns == defaultNetworkZoneColumns && c.flagAllProjects {
		c.flagColumns = "endu"
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkZoneColumn{}

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

func (c *cmdNetworkZoneList) projectColumnData(networkZone api.NetworkZone) string {
	return networkZone.Project
}

func (c *cmdNetworkZoneList) networkZoneNameColumnData(networkZone api.NetworkZone) string {
	return networkZone.Name
}

func (c *cmdNetworkZoneList) descriptionColumnData(networkZone api.NetworkZone) string {
	return networkZone.Description
}

func (c *cmdNetworkZoneList) usedByColumnData(networkZone api.NetworkZone) string {
	return fmt.Sprintf("%d", len(networkZone.UsedBy))
}

func (c *cmdNetworkZoneList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	var zones []api.NetworkZone
	if c.flagAllProjects {
		zones, err = d.GetNetworkZonesAllProjects()
		if err != nil {
			return err
		}
	} else {
		zones, err = d.GetNetworkZones()
		if err != nil {
			return err
		}
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, zone := range zones {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(zone))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, zones)
}

// Show.
type cmdNetworkZoneShow struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

var cmdNetworkZoneShowUsage = u.Usage{u.Zone.Remote()}

func (c *cmdNetworkZoneShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkZoneShowUsage...)
	cmd.Short = i18n.G("Show network zone configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network zone configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String

	// Show the network zone config.
	netZone, _, err := d.GetNetworkZone(zoneName)
	if err != nil {
		return err
	}

	sort.Strings(netZone.UsedBy)

	data, err := yaml.Marshal(&netZone)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdNetworkZoneGet struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagIsProperty bool
}

var cmdNetworkZoneGetUsage = u.Usage{u.Zone.Remote(), u.Key}

func (c *cmdNetworkZoneGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkZoneGetUsage...)
	cmd.Short = i18n.G("Get values for network zone configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network zone configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	key := parsed[1].String

	resp, _, err := d.GetNetworkZone(zoneName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network zone %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range resp.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Create.
type cmdNetworkZoneCreate struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagDescription string
}

var cmdNetworkZoneCreateUsage = u.Usage{u.NewName(u.Zone).Remote(), u.KV.List(0)}

func (c *cmdNetworkZoneCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkZoneCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network zones")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network zones"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network zone create z1

incus network zone create z1 < config.yaml
    Create network zone z1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Zone description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	var zonePut api.NetworkZonePut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &zonePut)
		if err != nil {
			return err
		}
	}

	// Create the network zone.
	zone := api.NetworkZonesPost{
		Name:           zoneName,
		NetworkZonePut: zonePut,
	}

	if zone.Config == nil {
		zone.Config = map[string]string{}
	}

	if c.flagDescription != "" {
		zone.Description = c.flagDescription
	}

	maps.Copy(zone.Config, keys)

	err = d.CreateNetworkZone(zone)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network zone %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Set.
type cmdNetworkZoneSet struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagIsProperty bool
}

var cmdNetworkZoneSetUsage = u.Usage{u.Zone.Remote(), u.LegacyKV.List(1)}

func (c *cmdNetworkZoneSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkZoneSetUsage...)
	cmd.Short = i18n.G("Set network zone configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network zone configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<Zone> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkZoneSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get the network zone.
	netZone, etag, err := d.GetNetworkZone(zoneName)
	if err != nil {
		return err
	}

	writable := netZone.Writable()
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

	return d.UpdateNetworkZone(zoneName, writable, etag)
}

func (c *cmdNetworkZoneSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkZoneUnset struct {
	global         *cmdGlobal
	networkZone    *cmdNetworkZone
	networkZoneSet *cmdNetworkZoneSet

	flagIsProperty bool
}

var cmdNetworkZoneUnsetUsage = u.Usage{u.Zone.Remote(), u.Key}

func (c *cmdNetworkZoneUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkZoneUnsetUsage...)
	cmd.Short = i18n.G("Unset network zone configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network zone configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkZoneSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkZoneSet, cmd, parsed)
}

// Edit.
type cmdNetworkZoneEdit struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

var cmdNetworkZoneEditUsage = u.Usage{u.Zone.Remote()}

func (c *cmdNetworkZoneEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkZoneEditUsage...)
	cmd.Short = i18n.G("Edit network zone configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network zone configurations as YAML"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network zone.
### Any line starting with a '# will be ignored.
###
### A network zone consists of a set of rules and configuration items.
###
### An example would look like:
### name: example.net
### description: Internal domain
### config:
###  user.foo: bah
`)
}

func (c *cmdNetworkZoneEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus network zone show` command to be passed in here, but only take the contents
		// of the NetworkZonePut fields when updating the Zone. The other fields are silently discarded.
		newdata := api.NetworkZone{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateNetworkZone(zoneName, newdata.NetworkZonePut, "")
	}

	// Get the current config.
	netZone, etag, err := d.GetNetworkZone(zoneName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&netZone)
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
		newdata := api.NetworkZone{} // We show the full Zone info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = d.UpdateNetworkZone(zoneName, newdata.Writable(), etag)
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
type cmdNetworkZoneDelete struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

var cmdNetworkZoneDeleteUsage = u.Usage{u.Zone.Remote().List(1)}

func (c *cmdNetworkZoneDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkZoneDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network zones")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network zones"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpNetworkZones(toComplete)
	}

	return cmd
}

func (c *cmdNetworkZoneDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		zoneName := p.RemoteObject.String

		// Delete the network zone.
		err = d.DeleteNetworkZone(zoneName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network Zone %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Add/Remove Rule.
type cmdNetworkZoneRecord struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneRecord) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("record")
	cmd.Short = i18n.G("Manage network zone records")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network zone records"))

	// List.
	networkZoneRecordListCmd := cmdNetworkZoneRecordList{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordListCmd.command())

	// Show.
	networkZoneRecordShowCmd := cmdNetworkZoneRecordShow{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordShowCmd.command())

	// Get.
	networkZoneRecordGetCmd := cmdNetworkZoneRecordGet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordGetCmd.command())

	// Create.
	networkZoneRecordCreateCmd := cmdNetworkZoneRecordCreate{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordCreateCmd.command())

	// Set.
	networkZoneRecordSetCmd := cmdNetworkZoneRecordSet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordSetCmd.command())

	// Unset.
	networkZoneRecordUnsetCmd := cmdNetworkZoneRecordUnset{global: c.global, networkZoneRecord: c, networkZoneRecordSet: &networkZoneRecordSetCmd}
	cmd.AddCommand(networkZoneRecordUnsetCmd.command())

	// Edit.
	networkZoneRecordEditCmd := cmdNetworkZoneRecordEdit{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEditCmd.command())

	// Delete.
	networkZoneRecordDeleteCmd := cmdNetworkZoneRecordDelete{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordDeleteCmd.command())

	// Entry.
	networkZoneRecordEntryCmd := cmdNetworkZoneRecordEntry{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEntryCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneRecordList struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagFormat string
}

var cmdNetworkZoneRecordListUsage = u.Usage{u.Zone.Remote()}

func (c *cmdNetworkZoneRecordList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkZoneRecordListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zone records")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List available network zone records"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String

	// List the records.
	records, err := d.GetNetworkZoneRecords(zoneName)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, record := range records {
		entries := []string{}

		for _, entry := range record.Entries {
			entries = append(entries, fmt.Sprintf("%s %s", entry.Type, entry.Value))
		}

		details := []string{
			record.Name,
			record.Description,
			strings.Join(entries, "\n"),
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("ENTRIES"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, records)
}

// Show.
type cmdNetworkZoneRecordShow struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

var cmdNetworkZoneRecordShowUsage = u.Usage{u.Zone.Remote(), u.Record}

func (c *cmdNetworkZoneRecordShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkZoneRecordShowUsage...)
	cmd.Short = i18n.G("Show network zone record configuration")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network zone record configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String

	// Show the network zone config.
	netRecord, _, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&netRecord)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdNetworkZoneRecordGet struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagIsProperty bool
}

var cmdNetworkZoneRecordGetUsage = u.Usage{u.Zone.Remote(), u.Record, u.Key}

func (c *cmdNetworkZoneRecordGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkZoneRecordGetUsage...)
	cmd.Short = i18n.G("Get values for network zone record configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network zone record configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone record property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkZoneRecordConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String
	key := parsed[2].String

	resp, _, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network zone record %q: %v"), key, recordName, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range resp.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Create.
type cmdNetworkZoneRecordCreate struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagDescription string
}

var cmdNetworkZoneRecordCreateUsage = u.Usage{u.Zone.Remote(), u.NewName(u.Record), u.KV.List(0)}

func (c *cmdNetworkZoneRecordCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkZoneRecordCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network zone record")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network zone record"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network zone record create z1 r1

incus network zone record create z1 r1 < config.yaml
    Create record r1 for zone z1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Record description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	var recordPut api.NetworkZoneRecordPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &recordPut)
		if err != nil {
			return err
		}
	}

	// Create the network zone.
	record := api.NetworkZoneRecordsPost{
		Name:                 recordName,
		NetworkZoneRecordPut: recordPut,
	}

	if record.Config == nil {
		record.Config = map[string]string{}
	}

	if c.flagDescription != "" {
		record.Description = c.flagDescription
	}

	maps.Copy(record.Config, keys)

	err = d.CreateNetworkZoneRecord(zoneName, record)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network zone record %s created")+"\n", recordName)
	}

	return nil
}

// Set.
type cmdNetworkZoneRecordSet struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagIsProperty bool
}

var cmdNetworkZoneRecordSetUsage = u.Usage{u.Zone.Remote(), u.Record, u.LegacyKV.List(1)}

func (c *cmdNetworkZoneRecordSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkZoneRecordSetUsage...)
	cmd.Short = i18n.G("Set network zone record configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network zone record configuration keys`))

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone record property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkZoneRecordSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// Get the network zone.
	netRecord, etag, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	writable := netRecord.Writable()
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

	return d.UpdateNetworkZoneRecord(zoneName, recordName, writable, etag)
}

func (c *cmdNetworkZoneRecordSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkZoneRecordUnset struct {
	global               *cmdGlobal
	networkZoneRecord    *cmdNetworkZoneRecord
	networkZoneRecordSet *cmdNetworkZoneRecordSet

	flagIsProperty bool
}

var cmdNetworkZoneRecordUnsetUsage = u.Usage{u.Zone.Remote(), u.Record, u.Key}

func (c *cmdNetworkZoneRecordUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkZoneRecordUnsetUsage...)
	cmd.Short = i18n.G("Unset network zone record configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network zone record configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone record property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkZoneRecordConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkZoneRecordSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkZoneRecordSet, cmd, parsed)
}

// Edit.
type cmdNetworkZoneRecordEdit struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

var cmdNetworkZoneRecordEditUsage = u.Usage{u.Zone.Remote(), u.Record}

func (c *cmdNetworkZoneRecordEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkZoneRecordEditUsage...)
	cmd.Short = i18n.G("Edit network zone record configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network zone record configurations as YAML"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network zone record.
### Any line starting with a '# will be ignored.
###
### A network zone consists of a set of rules and configuration items.
###
### An example would look like:
### name: foo
### description: SPF record
### config:
###  user.foo: bah
`)
}

func (c *cmdNetworkZoneRecordEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus network zone show` command to be passed in here, but only take the contents
		// of the NetworkZonePut fields when updating the Zone. The other fields are silently discarded.
		newdata := api.NetworkZoneRecord{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateNetworkZoneRecord(zoneName, recordName, newdata.NetworkZoneRecordPut, "")
	}

	// Get the current config.
	netRecord, etag, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(netRecord.Writable())
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
		newdata := api.NetworkZoneRecord{} // We show the full Zone info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = d.UpdateNetworkZoneRecord(zoneName, recordName, newdata.Writable(), etag)
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
type cmdNetworkZoneRecordDelete struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

var cmdNetworkZoneRecordDeleteUsage = u.Usage{u.Zone.Remote(), u.Record}

func (c *cmdNetworkZoneRecordDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkZoneRecordDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network zone record")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network zone record"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String

	// Delete the network zone.
	err = d.DeleteNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network zone record %s deleted")+"\n", recordName)
	}

	return nil
}

// Add/Remove Rule.
type cmdNetworkZoneRecordEntry struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagTTL uint64
}

func (c *cmdNetworkZoneRecordEntry) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("entry")
	cmd.Short = i18n.G("Manage network zone record entries")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network zone record entries"))

	// Rule Add.
	cmd.AddCommand(c.commandAdd())

	// Rule Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

var cmdNetworkZoneRecordEntryAddUsage = u.Usage{u.Zone.Remote(), u.Record, u.Type, u.Value}

func (c *cmdNetworkZoneRecordEntry) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkZoneRecordEntryAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add a network zone record entry")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add entries to a network zone record"))
	cmd.RunE = c.runAdd
	cmd.Flags().Uint64Var(&c.flagTTL, "ttl", 0, i18n.G("Entry TTL")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordEntry) runAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordEntryAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String
	entryType := parsed[2].String
	entryValue := parsed[3].String

	// Get the network record.
	netRecord, etag, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	// Add the entry.
	netRecord.Entries = append(netRecord.Entries, api.NetworkZoneRecordEntry{
		Type:  entryType,
		TTL:   c.flagTTL,
		Value: entryValue,
	})
	return d.UpdateNetworkZoneRecord(zoneName, recordName, netRecord.Writable(), etag)
}

var cmdNetworkZoneRecordEntryRemoveUsage = u.Usage{u.Zone.Remote(), u.Record, u.Type, u.Value}

func (c *cmdNetworkZoneRecordEntry) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkZoneRecordEntryRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove a network zone record entry")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove entries from a network zone record"))
	cmd.RunE = c.runRemove

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkZones(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordEntry) runRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkZoneRecordEntryRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	zoneName := parsed[0].RemoteObject.String
	recordName := parsed[1].String
	entryType := parsed[2].String
	entryValue := parsed[3].String

	// Get the network zone record.
	netRecord, etag, err := d.GetNetworkZoneRecord(zoneName, recordName)
	if err != nil {
		return err
	}

	found := false
	for i, entry := range netRecord.Entries {
		if entry.Type != entryType || entry.Value != entryValue {
			continue
		}

		found = true
		netRecord.Entries = slices.Delete(netRecord.Entries, i, i+1)
		break
	}

	if !found {
		return errors.New(i18n.G("Couldn't find a matching entry"))
	}

	return d.UpdateNetworkZoneRecord(zoneName, recordName, netRecord.Writable(), etag)
}
