package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdConfigTemplate struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigTemplate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("template")
	cmd.Short = i18n.G("Manage instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance file templates`))

	// Create
	configTemplateCreateCmd := cmdConfigTemplateCreate{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateCreateCmd.Command())

	// Delete
	configTemplateDeleteCmd := cmdConfigTemplateDelete{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateDeleteCmd.Command())

	// Edit
	configTemplateEditCmd := cmdConfigTemplateEdit{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateEditCmd.Command())

	// List
	configTemplateListCmd := cmdConfigTemplateList{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateListCmd.Command())

	// Show
	configTemplateShowCmd := cmdConfigTemplateShow{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdConfigTemplateCreate struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Create new instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new instance file templates`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus config template create u1 t1

incus config template create u1 t1 < config.tpl
    Create template t1 for instance u1 from config.tpl`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateCreate) Run(cmd *cobra.Command, args []string) error {
	var stdinData io.ReadSeeker

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

		// Reset the seek position
		stdinData = bytes.NewReader(contents)
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Create instance file template
	return resource.server.CreateInstanceTemplateFile(resource.name, args[1], stdinData)
}

// Delete.
type cmdConfigTemplateDelete struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instance file templates`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdConfigTemplateDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Delete instance file template
	return resource.server.DeleteInstanceTemplateFile(resource.name, args[1])
}

// Edit.
type cmdConfigTemplateEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Edit instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit instance file templates`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdConfigTemplateEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Edit instance file template
	if !termios.IsTerminal(getStdinFd()) {
		return resource.server.CreateInstanceTemplateFile(resource.name, args[1], os.Stdin)
	}

	reader, err := resource.server.GetInstanceTemplateFile(resource.name, args[1])
	if err != nil {
		return err
	}

	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err = textEditor("", content)
	if err != nil {
		return err
	}

	for {
		reader := bytes.NewReader(content)
		err := resource.server.CreateInstanceTemplateFile(resource.name, args[1], reader)
		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Error updating template file: %s")+"\n", err)
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

// List.
type cmdConfigTemplateList struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate

	flagFormat string
}

func (c *cmdConfigTemplateList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("List instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List instance file templates`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigTemplateList) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// List the templates
	templates, err := resource.server.GetInstanceTemplateFiles(resource.name)
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

	return cli.RenderTable(c.flagFormat, header, data, templates)
}

// Show.
type cmdConfigTemplateShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Show content of instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show content of instance file templates`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdConfigTemplateShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Show the template
	template, err := resource.server.GetInstanceTemplateFile(resource.name, args[1])
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
