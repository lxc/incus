package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdConfigTemplate struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigTemplate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("template")
	cmd.Short = i18n.G("Manage instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage instance file templates`))

	// Create
	configTemplateCreateCmd := cmdConfigTemplateCreate{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateCreateCmd.command())

	// Delete
	configTemplateDeleteCmd := cmdConfigTemplateDelete{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateDeleteCmd.command())

	// Edit
	configTemplateEditCmd := cmdConfigTemplateEdit{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateEditCmd.command())

	// List
	configTemplateListCmd := cmdConfigTemplateList{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateListCmd.command())

	// Show
	configTemplateShowCmd := cmdConfigTemplateShow{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdConfigTemplateCreate struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

var cmdConfigTemplateCreateUsage = u.Usage{u.Instance.Remote(), u.NewName(u.Template)}

func (c *cmdConfigTemplateCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdConfigTemplateCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create new instance file templates`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus config template create u1 t1

incus config template create u1 t1 < config.tpl
    Create template t1 for instance u1 from config.tpl`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigTemplateCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var stdinData io.ReadSeeker
	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	templateName := parsed[1].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Reset the seek position
		stdinData = bytes.NewReader(contents)
	}

	// Create instance file template
	return d.CreateInstanceTemplateFile(instanceName, templateName, stdinData)
}

// Delete.
type cmdConfigTemplateDelete struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

var cmdConfigTemplateDeleteUsage = u.Usage{u.Instance.Remote(), u.Template}

func (c *cmdConfigTemplateDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdConfigTemplateDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete instance file templates`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceConfigTemplates(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigTemplateDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	templateName := parsed[1].String

	// Delete instance file template
	return d.DeleteInstanceTemplateFile(instanceName, templateName)
}

// Edit.
type cmdConfigTemplateEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

var cmdConfigTemplateEditUsage = u.Usage{u.Instance.Remote(), u.Template}

func (c *cmdConfigTemplateEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdConfigTemplateEditUsage...)
	cmd.Short = i18n.G("Edit instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit instance file templates`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceConfigTemplates(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigTemplateEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	templateName := parsed[1].String

	// Edit instance file template
	if !termios.IsTerminal(getStdinFd()) {
		return d.CreateInstanceTemplateFile(instanceName, templateName, os.Stdin)
	}

	reader, err := d.GetInstanceTemplateFile(instanceName, templateName)
	if err != nil {
		return err
	}

	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err = cli.TextEditor("", content)
	if err != nil {
		return err
	}

	for {
		reader := bytes.NewReader(content)
		err := d.CreateInstanceTemplateFile(instanceName, templateName, reader)
		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Error updating template file: %s")+"\n", err)
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

// List.
type cmdConfigTemplateList struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate

	flagFormat string
}

var cmdConfigTemplateListUsage = u.Usage{u.Instance.Remote()}

func (c *cmdConfigTemplateList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdConfigTemplateListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List instance file templates`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigTemplateListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	// List the templates
	templates, err := d.GetInstanceTemplateFiles(instanceName)
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, template := range templates {
		data = append(data, []string{template})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("FILENAME"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, templates)
}

// Show.
type cmdConfigTemplateShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

var cmdConfigTemplateShowUsage = u.Usage{u.Instance.Remote(), u.Template}

func (c *cmdConfigTemplateShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdConfigTemplateShowUsage...)
	cmd.Short = i18n.G("Show content of instance file templates")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show content of instance file templates`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceConfigTemplates(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigTemplateShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	templateName := parsed[1].String

	// Show the template
	template, err := d.GetInstanceTemplateFile(instanceName, templateName)
	if err != nil {
		return err
	}

	content, err := io.ReadAll(template)
	if err != nil {
		return err
	}

	fmt.Printf("%s", content)

	return nil
}
