package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
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

type cmdNetworkACL struct {
	global *cmdGlobal
}

func (c *cmdNetworkACL) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("acl")
	cmd.Short = i18n.G("Manage network ACLs")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network ACLs"))

	// List.
	networkACLListCmd := cmdNetworkACLList{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLListCmd.command())

	// Show.
	networkACLShowCmd := cmdNetworkACLShow{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLShowCmd.command())

	// Show log.
	networkACLShowLogCmd := cmdNetworkACLShowLog{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLShowLogCmd.command())

	// Get.
	networkACLGetCmd := cmdNetworkACLGet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLGetCmd.command())

	// Create.
	networkACLCreateCmd := cmdNetworkACLCreate{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLCreateCmd.command())

	// Set.
	networkACLSetCmd := cmdNetworkACLSet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLSetCmd.command())

	// Unset.
	networkACLUnsetCmd := cmdNetworkACLUnset{global: c.global, networkACL: c, networkACLSet: &networkACLSetCmd}
	cmd.AddCommand(networkACLUnsetCmd.command())

	// Edit.
	networkACLEditCmd := cmdNetworkACLEdit{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLEditCmd.command())

	// Rename.
	networkACLRenameCmd := cmdNetworkACLRename{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLRenameCmd.command())

	// Delete.
	networkACLDeleteCmd := cmdNetworkACLDelete{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLDeleteCmd.command())

	// Rule.
	networkACLRuleCmd := cmdNetworkACLRule{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLRuleCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkACLList struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagFormat      string
	flagAllProjects bool
}

var cmdNetworkACLListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdNetworkACLList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkACLListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network ACLS")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List available network ACL"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List network ACLs across all projects"))

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

func (c *cmdNetworkACLList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	var acls []api.NetworkACL
	if c.flagAllProjects {
		acls, err = d.GetNetworkACLsAllProjects()
		if err != nil {
			return err
		}
	} else {
		acls, err = d.GetNetworkACLs()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, acl := range acls {
		strUsedBy := fmt.Sprintf("%d", len(acl.UsedBy))
		details := []string{
			acl.Name,
			acl.Description,
			strUsedBy,
		}

		if c.flagAllProjects {
			details = append([]string{acl.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}

	if c.flagAllProjects {
		header = append([]string{i18n.G("PROJECT")}, header...)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, acls)
}

// Show.
type cmdNetworkACLShow struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

var cmdNetworkACLShowUsage = u.Usage{u.ACL.Remote()}

func (c *cmdNetworkACLShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkACLShowUsage...)
	cmd.Short = i18n.G("Show network ACL configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network ACL configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String

	// Show the network ACL config.
	netACL, _, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	sort.Strings(netACL.UsedBy)

	data, err := yaml.Dump(&netACL, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Show log.
type cmdNetworkACLShowLog struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

var cmdNetworkACLShowLogUsage = u.Usage{u.ACL.Remote()}

func (c *cmdNetworkACLShowLog) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show-log", cmdNetworkACLShowLogUsage...)
	cmd.Short = i18n.G("Show network ACL log")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Show network ACL log"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLShowLog) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLShowLogUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String

	// Get the ACL log.
	log, err := d.GetNetworkACLLogfile(aclName)
	if err != nil {
		return err
	}

	_, err = io.Copy(os.Stdout, log)
	_ = log.Close()

	return err
}

// Get.
type cmdNetworkACLGet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagIsProperty bool
}

var cmdNetworkACLGetUsage = u.Usage{u.ACL.Remote(), u.Key}

func (c *cmdNetworkACLGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkACLGetUsage...)
	cmd.Short = i18n.G("Get values for network ACL configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Get values for network ACL configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network ACL property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkACLConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	key := parsed[1].String

	resp, _, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network ACL %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
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
type cmdNetworkACLCreate struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagDescription string
}

var cmdNetworkACLCreateUsage = u.Usage{u.NewName(u.ACL).Remote(), u.KV.List(0)}

func (c *cmdNetworkACLCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkACLCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new network ACLs")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Create new network ACLs"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network acl create a1

incus network acl create a1 < config.yaml
    Create network acl with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Network ACL description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	var aclPut api.NetworkACLPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		err = loader.Load(&aclPut)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	// Create the network ACL.
	acl := api.NetworkACLsPost{
		NetworkACLPost: api.NetworkACLPost{
			Name: aclName,
		},
		NetworkACLPut: aclPut,
	}

	if c.flagDescription != "" {
		acl.Description = c.flagDescription
	}

	if acl.Config == nil {
		acl.Config = map[string]string{}
	}

	maps.Copy(acl.Config, keys)

	err = d.CreateNetworkACL(acl)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network ACL %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Set.
type cmdNetworkACLSet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagIsProperty bool
}

var cmdNetworkACLSetUsage = u.Usage{u.ACL.Remote(), u.LegacyKV.List(1)}

func (c *cmdNetworkACLSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkACLSetUsage...)
	cmd.Short = i18n.G("Set network ACL configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network ACL configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<ACL> <key> <value>`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network ACL property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkACLSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get the network ACL.
	netACL, etag, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	writable := netACL.Writable()
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

	return d.UpdateNetworkACL(aclName, writable, etag)
}

func (c *cmdNetworkACLSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdNetworkACLUnset struct {
	global        *cmdGlobal
	networkACL    *cmdNetworkACL
	networkACLSet *cmdNetworkACLSet

	flagIsProperty bool
}

var cmdNetworkACLUnsetUsage = u.Usage{u.ACL.Remote(), u.Key}

func (c *cmdNetworkACLUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkACLUnsetUsage...)
	cmd.Short = i18n.G("Unset network ACL configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset network ACL configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network ACL property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkACLConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkACLSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkACLSet, cmd, parsed)
}

// Edit.
type cmdNetworkACLEdit struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

var cmdNetworkACLEditUsage = u.Usage{u.ACL.Remote()}

func (c *cmdNetworkACLEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkACLEditUsage...)
	cmd.Short = i18n.G("Edit network ACL configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Edit network ACL configurations as YAML"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network ACL.
### Any line starting with a '# will be ignored.
###
### A network ACL consists of a set of rules and configuration items.
###
### An example would look like:
### name: allow-all-inbound
### description: test desc
### egress: []
### ingress:
### - action: allow
###   state: enabled
###   protocol: ""
###   source: ""
###   source_port: ""
###   destination: ""
###   destination_port: ""
###   icmp_type: ""
###   icmp_code: ""
### config:
###  user.foo: bah
###
### Note that only the ingress and egress rules, description and configuration keys can be changed.`)
}

func (c *cmdNetworkACLEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		// Allow output of `incus network acl show` command to be passed in here, but only take the contents
		// of the NetworkACLPut fields when updating the ACL. The other fields are silently discarded.
		newdata := api.NetworkACL{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateNetworkACL(aclName, newdata.NetworkACLPut, "")
	}

	// Get the current config.
	netACL, etag, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&netACL, yaml.V2)
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
		newdata := api.NetworkACL{} // We show the full ACL info, but only send the writable fields.
		err = yaml.Load(content, &newdata, yaml.WithKnownFields())
		if err == nil {
			err = d.UpdateNetworkACL(aclName, newdata.Writable(), etag)
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

// Rename.
type cmdNetworkACLRename struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

var cmdNetworkACLRenameUsage = u.Usage{u.ACL.Remote(), u.NewName(u.ACL)}

func (c *cmdNetworkACLRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdNetworkACLRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network ACLs")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Rename network ACLs"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	newACLName := parsed[1].String

	// Rename the network.
	err = d.RenameNetworkACL(aclName, api.NetworkACLPost{Name: newACLName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network ACL %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newACLName)
	}

	return nil
}

// Delete.
type cmdNetworkACLDelete struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

var cmdNetworkACLDeleteUsage = u.Usage{u.ACL.Remote().List(1)}

func (c *cmdNetworkACLDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkACLDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete network ACLs")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Delete network ACLs"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpNetworkACLs(toComplete)
	}

	return cmd
}

func (c *cmdNetworkACLDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		aclName := p.RemoteObject.String

		// Delete the network ACL.
		err = d.DeleteNetworkACL(aclName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network ACL %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Add/Remove Rule.
type cmdNetworkACLRule struct {
	global          *cmdGlobal
	networkACL      *cmdNetworkACL
	flagRemoveForce bool
	flagDescription string
}

func (c *cmdNetworkACLRule) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rule")
	cmd.Short = i18n.G("Manage network ACL rules")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Manage network ACL rules"))

	// Rule Add.
	cmd.AddCommand(c.commandAdd())

	// Rule Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

var cmdNetworkACLRuleAddUsage = u.Usage{u.ACL.Remote(), u.Direction, u.LegacyKV.List(1)}

func (c *cmdNetworkACLRule) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdNetworkACLRuleAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add rules to an ACL")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Add rules to an ACL"))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Rule description")+"``")

	cmd.RunE = c.runAdd

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		if len(args) == 1 {
			return []string{"ingress", "egress"}, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 2 {
			return c.global.cmpNetworkACLRuleProperties()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// networkACLRuleJSONStructFieldMap returns a map of JSON tag names to struct field indices for api.NetworkACLRule.
func networkACLRuleJSONStructFieldMap() map[string]int {
	// Use reflect to get field names in rule from json tags.
	ruleType := reflect.TypeOf(api.NetworkACLRule{})
	allowedKeys := make(map[string]int, ruleType.NumField())

	for i := range ruleType.NumField() {
		field := ruleType.Field(i)
		if field.PkgPath != "" {
			continue // Skip unexported fields. It is empty for upper case (exported) field names.
		}

		if field.Type.Name() != "string" {
			continue // Skip non-string fields.
		}

		// Split the json tag into its name and options (e.g. json:"action,omitempty").
		tagParts := strings.SplitN(string(field.Tag.Get(("json"))), ",", 2)
		fieldName := tagParts[0]

		if fieldName == "" {
			continue // Skip fields with no tagged field name.
		}

		allowedKeys[fieldName] = i // Add the name to allowed keys and record field index.
	}

	return allowedKeys
}

// parseConfigKeysToRule converts a map of key/value pairs into an api.NetworkACLRule using reflection.
func (c *cmdNetworkACLRule) parseConfigToRule(config map[string]string) (*api.NetworkACLRule, error) {
	// Use reflect to get struct field indices in NetworkACLRule for json tags.
	allowedKeys := networkACLRuleJSONStructFieldMap()

	// Initialize new rule.
	rule := api.NetworkACLRule{}
	ruleValue := reflect.ValueOf(&rule).Elem()

	for k, v := range config {
		fieldIndex, found := allowedKeys[k]
		if !found {
			return nil, fmt.Errorf(i18n.G("Unknown key: %s"), k)
		}

		fieldValue := ruleValue.Field(fieldIndex)
		if !fieldValue.CanSet() {
			return nil, fmt.Errorf(i18n.G("Cannot set key: %s"), k)
		}

		fieldValue.SetString(v) // Set the value into the struct field.
	}

	return &rule, nil
}

func (c *cmdNetworkACLRule) runAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLRuleAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	direction := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// Get the network ACL.
	netACL, etag, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	rule, err := c.parseConfigToRule(keys)
	if err != nil {
		return err
	}

	if c.flagDescription != "" {
		rule.Description = c.flagDescription
	}

	rule.Normalise() // Strip space.

	// Default to enabled if not specified.
	if rule.State == "" {
		rule.State = "enabled"
	}

	// Add rule to the requested direction (if direction valid).
	switch direction {
	case "ingress":
		netACL.Ingress = append(netACL.Ingress, *rule)
	case "egress":
		netACL.Egress = append(netACL.Egress, *rule)
	}

	return d.UpdateNetworkACL(aclName, netACL.Writable(), etag)
}

var cmdNetworkACLRuleRemoveUsage = u.Usage{u.ACL.Remote(), u.Direction, u.LegacyKV.List(0)}

func (c *cmdNetworkACLRule) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdNetworkACLRuleRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove rules from an ACL")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Remove rules from an ACL"))
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, i18n.G("Remove all rules that match"))

	cmd.RunE = c.runRemove

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworkACLs(toComplete)
		}

		if len(args) == 1 {
			return []string{"ingress", "egress"}, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 2 {
			return c.global.cmpNetworkACLRuleProperties()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLRule) runRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkACLRuleRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aclName := parsed[0].RemoteObject.String
	direction := parsed[1].String
	filters, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// Get the network ACL.
	netACL, etag, err := d.GetNetworkACL(aclName)
	if err != nil {
		return err
	}

	// Use reflect to get struct field indices in NetworkACLRule for json tags.
	allowedKeys := networkACLRuleJSONStructFieldMap()

	// Check the supplied filters match possible fields.
	for k := range filters {
		_, found := allowedKeys[k]
		if !found {
			return fmt.Errorf(i18n.G("Unknown key: %s"), k)
		}
	}

	// isFilterMatch returns whether the supplied rule has matching field values in the filters supplied.
	// If no filters are supplied, then the rule is considered to have matched.
	isFilterMatch := func(rule *api.NetworkACLRule, filters map[string]string) bool {
		ruleValue := reflect.ValueOf(rule).Elem()

		for k, v := range filters {
			fieldIndex, found := allowedKeys[k]
			if !found {
				return false
			}

			fieldValue := ruleValue.Field(fieldIndex)
			if fieldValue.String() != v {
				return false
			}
		}

		return true // Match found as all struct fields match the supplied filter values.
	}

	// removeFromRules removes a single rule that matches the filters supplied. If multiple rules match then
	// an error is returned unless c.flagRemoveForce is true, in which case all matching rules are removed.
	removeFromRules := func(rules []api.NetworkACLRule, filters map[string]string) ([]api.NetworkACLRule, error) {
		removed := false
		newRules := make([]api.NetworkACLRule, 0, len(rules))

		for _, r := range rules {
			if isFilterMatch(&r, filters) {
				if removed && !c.flagRemoveForce {
					return nil, errors.New(i18n.G("Multiple rules match. Use --force to remove them all"))
				}

				removed = true
				continue // Don't add removed rule to newRules.
			}

			newRules = append(newRules, r)
		}

		if !removed {
			return nil, errors.New(i18n.G("No matching rule(s) found"))
		}

		return newRules, nil
	}

	// Remove matching rule(s) from the requested direction (if direction valid).
	switch direction {
	case "ingress":
		rules, err := removeFromRules(netACL.Ingress, filters)
		if err != nil {
			return err
		}

		netACL.Ingress = rules
	case "egress":
		rules, err := removeFromRules(netACL.Egress, filters)
		if err != nil {
			return err
		}

		netACL.Egress = rules
	}

	return d.UpdateNetworkACL(aclName, netACL.Writable(), etag)
}
