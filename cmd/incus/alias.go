package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdAlias struct {
	global *cmdGlobal
}

// Command is a method of the cmdAlias structure that returns a new cobra Command for managing command aliases.
// This includes commands for adding, listing, renaming, and removing aliases, along with their usage and descriptions.
func (c *cmdAlias) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("alias")
	cmd.Short = i18n.G("Manage command aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage command aliases`))
	cmd.Hidden = true

	// Add
	aliasAddCmd := cmdAliasAdd{global: c.global, alias: c}
	cmd.AddCommand(aliasAddCmd.Command())

	// List
	aliasListCmd := cmdAliasList{global: c.global, alias: c}
	cmd.AddCommand(aliasListCmd.Command())

	// Rename
	aliasRenameCmd := cmdAliasRename{global: c.global, alias: c}
	cmd.AddCommand(aliasRenameCmd.Command())

	// Remove
	aliasRemoveCmd := cmdAliasRemove{global: c.global, alias: c}
	cmd.AddCommand(aliasRemoveCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdAliasAdd struct {
	global *cmdGlobal
	alias  *cmdAlias
}

var cmdAliasAddUsage = u.Usage{u.NewName(u.Alias), u.Target(u.Placeholder(i18n.G("command")))}

// Command is a method of the cmdAliasAdd structure that returns a new cobra Command for adding new command aliases.
// It specifies the command usage, description, and examples, and links it to the RunE method for execution logic.
func (c *cmdAliasAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdAliasAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add new aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.`))

	cmd.RunE = c.Run

	return cmd
}

// Run is a method of the cmdAliasAdd structure. It implements the logic to add a new alias command.
// The function checks for valid arguments, verifies if the alias already exists, and if not, adds the new alias to the configuration.
func (c *cmdAliasAdd) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	parsed, err := cmdAliasAddUsage.Parse(conf, cmd, args)
	if err != nil {
		return err
	}

	alias := parsed[0].String

	// Look for an existing alias
	_, ok := conf.Aliases[alias]
	if ok {
		return fmt.Errorf(i18n.G("Alias %s already exists"), alias)
	}

	// Add the new alias
	conf.Aliases[alias] = parsed[1].String

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}

// List.
type cmdAliasList struct {
	global *cmdGlobal
	alias  *cmdAlias

	flagFormat string
}

var cmdAliasListUsage = u.Usage{}

// Command is a method of the cmdAliasList structure that returns a new cobra Command for listing command aliases.
// It specifies the command usage, description, aliases, and output formatting options, and links it to the RunE method for execution logic.
func (c *cmdAliasList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdAliasListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.Run

	return cmd
}

// Run is a method of the cmdAliasList structure. It implements the logic to list existing command aliases.
// The function checks for valid arguments, collects all the aliases, sorts them, and renders them in the specified format.
func (c *cmdAliasList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	_, err := cmdAliasListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	// List the aliases
	data := [][]string{}
	for k, v := range conf.Aliases {
		data = append(data, []string{k, v})
	}

	// Apply default entries.
	for k, v := range defaultAliases {
		_, ok := conf.Aliases[k]
		if !ok {
			data = append(data, []string{k, v})
		}
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("ALIAS"),
		i18n.G("TARGET"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, conf.Aliases)
}

// Rename.
type cmdAliasRename struct {
	global *cmdGlobal
	alias  *cmdAlias
}

var cmdAliasRenameUsage = u.Usage{u.Alias, u.NewName(u.Alias)}

// Command is a method of the cmdAliasRename structure. It returns a new cobra.Command object.
// This command allows a user to rename existing aliases in the CLI application.
func (c *cmdAliasRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdAliasRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias rename list my-list
    Rename existing alias "list" to "my-list".`))

	cmd.RunE = c.Run

	return cmd
}

// Run is a method of the cmdAliasRename structure. It takes a cobra command and a slice of strings as arguments.
// This method checks the validity of arguments, ensures the existence of the old alias, verifies the non-existence of the new alias, and then proceeds to rename the alias in the configuration.
func (c *cmdAliasRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	parsed, err := cmdAliasRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	alias := parsed[0].String
	newAlias := parsed[1].String

	// Check for the existing alias
	target, ok := conf.Aliases[alias]
	if !ok {
		return fmt.Errorf(i18n.G("Alias %s doesn't exist"), alias)
	}

	// Check for the new alias
	_, ok = conf.Aliases[newAlias]
	if ok {
		return fmt.Errorf(i18n.G("Alias %s already exists"), newAlias)
	}

	// Rename the alias
	conf.Aliases[newAlias] = target
	delete(conf.Aliases, alias)

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}

// Remove.
type cmdAliasRemove struct {
	global *cmdGlobal
	alias  *cmdAlias
}

var cmdAliasRemoveUsage = u.Usage{u.Alias}

// Command is a method of the cmdAliasRemove structure. It configures and returns a cobra.Command object.
// This command enables the removal of a given alias from the command line interface.
func (c *cmdAliasRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdAliasRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias remove my-list
    Remove the "my-list" alias.`))

	cmd.RunE = c.Run

	return cmd
}

// Run is a method of the cmdAliasRemove structure that executes the actual operation of the alias removal command.
// It takes as input the name of the alias to be removed and updates the global configuration file to reflect this change.
func (c *cmdAliasRemove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	parsed, err := cmdAliasRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	alias := parsed[0].String

	// Look for the alias
	_, ok := conf.Aliases[alias]
	if !ok {
		return fmt.Errorf(i18n.G("Alias %s doesn't exist"), alias)
	}

	// Delete the alias
	delete(conf.Aliases, alias)

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}
