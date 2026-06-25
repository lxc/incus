package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdDefault struct {
	global *cmdGlobal
}

func (c *cmdDefault) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("default")
	cmd.Short = i18n.G("Manage client defaults")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage client defaults`))
	cmd.Hidden = true

	// Set
	defaultSetCmd := cmdDefaultSet{global: c.global, def: c}
	cmd.AddCommand(defaultSetCmd.command())

	// Show
	defaultShowCmd := cmdDefaultShow{global: c.global, def: c}
	cmd.AddCommand(defaultShowCmd.command())

	// Unset
	defaultUnsetCmd := cmdDefaultUnset{global: c.global, def: c, defaultSet: &defaultSetCmd}
	cmd.AddCommand(defaultUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Set.
type cmdDefaultSet struct {
	global *cmdGlobal
	def    *cmdDefault
}

var cmdDefaultSetUsage = u.Usage{u.KV.List(1)}

func (c *cmdDefaultSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdDefaultSetUsage...)
	cmd.Short = i18n.G("Set client defaults")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Set client defaults`))

	cmd.RunE = c.run

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdDefaultSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	conf := c.global.conf

	keys, err := kvToMap(parsed[0])
	if err != nil {
		return err
	}

	for k, v := range keys {
		switch k {
		case "list_format":
			err = c.setListFormat(v)
		case "console_type":
			err = c.setConsoleType(v)
		case "console_spice_command":
			err = c.setConsoleSpiceCommand(v)
		case "no_color":
			err = c.setNoColor(v)
		default:
			err = fmt.Errorf(i18n.G("Invalid default %q"), k)
		}

		if err != nil {
			return err
		}
	}

	return conf.SaveConfig(c.global.confPath)
}

func (c *cmdDefaultSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdDefaultSetUsage, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

func (c *cmdDefaultSet) setListFormat(listFormat string) error {
	// Validate list format
	switch listFormat {
	case cli.TableFormatCSV, cli.TableFormatJSON, cli.TableFormatTable, cli.TableFormatYAML, cli.TableFormatCompact, cli.TableFormatMarkdown, "":
	default:
		return fmt.Errorf(i18n.G("Invalid value %q for list_format"), listFormat)
	}

	c.global.conf.Defaults.ListFormat = listFormat

	return nil
}

func (c *cmdDefaultSet) setConsoleType(consoleType string) error {
	// Validate console type
	switch consoleType {
	case "console", "vga", "":
	default:
		return fmt.Errorf(i18n.G("Invalid value %q for console_type"), consoleType)
	}

	c.global.conf.Defaults.ConsoleType = consoleType

	return nil
}

func (c *cmdDefaultSet) setConsoleSpiceCommand(consoleSpiceCommand string) error {
	c.global.conf.Defaults.ConsoleSpiceCommand = consoleSpiceCommand

	return nil
}

func (c *cmdDefaultSet) setNoColor(noColorStr string) error {
	// Treat an empty value as unsetting the key
	if noColorStr == "" {
		noColorStr = "false"
	}

	// Validate no color input
	noColor, err := strconv.ParseBool(noColorStr)
	if err != nil {
		return errors.New(i18n.G("no_color must be boolean"))
	}

	c.global.conf.Defaults.NoColor = noColor

	return nil
}

// Show.
type cmdDefaultShow struct {
	global *cmdGlobal
	def    *cmdDefault
}

var cmdDefaultShowUsage = u.Usage{}

func (c *cmdDefaultShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdDefaultShowUsage...)
	cmd.Short = i18n.G("Show client defaults")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show client defaults`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdDefaultShow) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Get Defaults
	defaults := conf.Defaults

	// Display defaults
	data, err := yaml.Dump(&defaults, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdDefaultUnset struct {
	global     *cmdGlobal
	def        *cmdDefault
	defaultSet *cmdDefaultSet
}

var cmdDefaultUnsetUsage = u.Usage{u.Default.List(1)}

func (c *cmdDefaultUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdDefaultUnsetUsage...)
	cmd.Short = i18n.G("Unset client defaults")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Unset client defaults`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdDefaultUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdDefaultUnsetUsage, cmd, args)
	if err != nil {
		return err
	}

	return unsetKey(c.defaultSet, cmd, parsed)
}
