package main

import (
	"fmt"
	"maps"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdAlias struct {
	global *cmdGlobal
}

type aliasRowData struct {
	alias  string
	target string
}

func (c *cmdAlias) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("alias")
	cmd.Short = i18n.G("Manage command aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage command aliases`))
	cmd.Hidden = true

	// Add
	aliasAddCmd := cmdAliasAdd{global: c.global, alias: c}
	cmd.AddCommand(aliasAddCmd.command())

	// List
	aliasListCmd := cmdAliasList{global: c.global, alias: c, lister: &listable[aliasRowData]{global: c.global}}
	cmd.AddCommand(aliasListCmd.command())

	// Rename
	aliasRenameCmd := cmdAliasRename{global: c.global, alias: c}
	cmd.AddCommand(aliasRenameCmd.command())

	// Remove
	aliasRemoveCmd := cmdAliasRemove{global: c.global, alias: c}
	cmd.AddCommand(aliasRemoveCmd.command())

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

func (c *cmdAliasAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdAliasAddUsage...)
	cmd.Aliases = []string{"create"}
	cmd.Short = i18n.G("Add new aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Add new aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.

incus alias add volume "storage volume @ARGS@"
    Create a short command for managing volumes

incus alias add cat "exec @ARG1@ -- cat @ARG2@"
    Create a command for displaying file content of instances`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdAliasAdd) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	parsed, err := c.global.Parse(cmdAliasAddUsage, cmd, args)
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
	lister *listable[aliasRowData]
}

var cmdAliasListUsage = u.Usage{}

func (c *cmdAliasList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdAliasListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List aliases"))

	c.lister.init(cmd, "at", map[rune]listColumn[aliasRowData]{
		'a': {i18n.G("ALIAS"), i18n.G("Alias"), func(v aliasRowData) string { return v.alias }},
		't': {i18n.G("TARGET"), i18n.G("Target command"), func(v aliasRowData) string { return v.target }},
	})

	cmd.RunE = c.run

	return cmd
}

func (c *cmdAliasList) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	_, err := c.global.Parse(cmdAliasListUsage, cmd, args)
	if err != nil {
		return err
	}

	// List the aliases
	rawAliases := maps.Clone(defaultAliases)
	maps.Copy(rawAliases, conf.Aliases)
	aliases := make([]aliasRowData, 0, len(rawAliases))
	for k, v := range rawAliases {
		aliases = append(aliases, aliasRowData{k, v})
	}

	return c.lister.run(aliases, rawAliases)
}

// Rename.
type cmdAliasRename struct {
	global *cmdGlobal
	alias  *cmdAlias
}

var cmdAliasRenameUsage = u.Usage{u.Alias, u.NewName(u.Alias)}

func (c *cmdAliasRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdAliasRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias rename list my-list
    Rename existing alias "list" to "my-list".`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdAliasRename) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	parsed, err := c.global.Parse(cmdAliasRenameUsage, cmd, args)
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

func (c *cmdAliasRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdAliasRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus alias remove my-list
    Remove the "my-list" alias.`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdAliasRemove) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	parsed, err := c.global.Parse(cmdAliasRemoveUsage, cmd, args)
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
