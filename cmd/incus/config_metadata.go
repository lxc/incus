package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/termios"
)

type cmdConfigMetadata struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigMetadata) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("metadata")
	cmd.Short = i18n.G("Manage instance metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance metadata files`))

	// Edit
	configMetadataEditCmd := cmdConfigMetadataEdit{global: c.global, config: c.config, configMetadata: c}
	cmd.AddCommand(configMetadataEditCmd.Command())

	// Show
	configMetadataShowCmd := cmdConfigMetadataShow{global: c.global, config: c.config, configMetadata: c}
	cmd.AddCommand(configMetadataShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Edit.
type cmdConfigMetadataEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configMetadata *cmdConfigMetadata
}

func (c *cmdConfigMetadataEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Edit instance metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit instance metadata files`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigMetadataEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the instance metadata.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
###
### architecture: x86_64
### creation_date: 1477146654
### expiry_date: 0
### properties:
###   architecture: x86_64
###   description: BusyBox x86_64
###   name: busybox-x86_64
###   os: BusyBox
### templates:
###   /template:
###     when:
###     - ""
###     create_only: false
###     template: template.tpl
###     properties: {}`)
}

func (c *cmdConfigMetadataEdit) Run(cmd *cobra.Command, args []string) error {
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

	// Edit the metadata
	if !termios.IsTerminal(getStdinFd()) {
		metadata := api.ImageMetadata{}
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(content, &metadata)
		if err != nil {
			return err
		}

		return resource.server.UpdateInstanceMetadata(resource.name, metadata, "")
	}

	metadata, etag, err := resource.server.GetInstanceMetadata(resource.name)
	if err != nil {
		return err
	}

	origContent, err := yaml.Marshal(metadata)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(origContent)))
	if err != nil {
		return err
	}

	for {
		metadata := api.ImageMetadata{}
		err = yaml.Unmarshal(content, &metadata)
		if err == nil {
			err = resource.server.UpdateInstanceMetadata(resource.name, metadata, etag)
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

// Show.
type cmdConfigMetadataShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configMetadata *cmdConfigMetadata
}

func (c *cmdConfigMetadataShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Show instance metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show instance metadata files`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigMetadataShow) Run(cmd *cobra.Command, args []string) error {
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

	// Show the instance metadata
	metadata, _, err := resource.server.GetInstanceMetadata(resource.name)
	if err != nil {
		return err
	}

	content, err := yaml.Marshal(metadata)
	if err != nil {
		return err
	}

	fmt.Printf("%s", content)

	return nil
}
