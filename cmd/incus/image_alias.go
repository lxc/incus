package main

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdImageAlias struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageAlias) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("alias")
	cmd.Short = i18n.G("Manage image aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage image aliases`))

	// Create
	imageAliasCreateCmd := cmdImageAliasCreate{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasCreateCmd.command())

	// Delete
	imageAliasDeleteCmd := cmdImageAliasDelete{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasDeleteCmd.command())

	// List
	imageAliasListCmd := cmdImageAliasList{global: c.global, image: c.image, imageAlias: c, lister: &listable[api.ImageAliasesEntry]{global: c.global}}
	cmd.AddCommand(imageAliasListCmd.command())

	// Rename
	imageAliasRenameCmd := cmdImageAliasRename{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasRenameCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdImageAliasCreate struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias

	flagDescription string
}

var cmdImageAliasCreateUsage = u.Usage{u.NewName(u.Alias).Remote(), u.Fingerprint}

func (c *cmdImageAliasCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdImageAliasCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create aliases for existing images")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create aliases for existing images`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagDescription, "description", "", "", i18n.G("Image alias description"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, true)
		}

		remote, _, found := strings.Cut(args[0], ":")
		if !found {
			remote = ""
		}

		return c.global.cmpImageFingerprintsFromRemote(toComplete, remote)
	}

	return cmd
}

func (c *cmdImageAliasCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdImageAliasCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aliasName := parsed[0].RemoteObject.String
	fingerprint := parsed[1].String

	// Create the alias
	alias := api.ImageAliasesPost{}
	alias.Name = aliasName
	alias.Target = fingerprint
	alias.Description = c.flagDescription

	return d.CreateImageAlias(alias)
}

// Delete.
type cmdImageAliasDelete struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

var cmdImageAliasDeleteUsage = u.Usage{u.Alias.Remote().List(1)}

func (c *cmdImageAliasDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdImageAliasDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete image aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete image aliases`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageAliasDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdImageAliasDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		aliasName := p.RemoteObject.String

		// Delete the alias
		err = d.DeleteImageAlias(aliasName)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// List.
type cmdImageAliasList struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
	lister     *listable[api.ImageAliasesEntry]
}

var cmdImageAliasListUsage = u.Usage{u.Colon(u.Remote).Optional(), u.Filter.List(0)}

func (c *cmdImageAliasList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdImageAliasListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List image aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List image aliases

Filters may be part of the image hash or part of the image alias name.`))

	c.lister.init(cmd, "aftd", map[rune]listColumn[api.ImageAliasesEntry]{
		'a': {i18n.G("ALIAS"), i18n.G("Alias"), c.imageAliasNameColumnData},
		'f': {i18n.G("FINGERPRINT"), i18n.G("Fingerprint (short)"), c.targetColumnData},
		'F': {i18n.G("FINGERPRINT"), i18n.G("Fingerprint (long)"), c.targetFullColumnData},
		't': {i18n.G("TYPE"), i18n.G("Type"), c.typeColumnData},
		'd': {i18n.G("DESCRIPTION"), i18n.G("Description"), c.descriptionColumntData},
	})

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, true)
	}

	return cmd
}

func (c *cmdImageAliasList) aliasShouldShow(filters []string, state *api.ImageAliasesEntry) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		if strings.Contains(state.Name, filter) || strings.Contains(state.Target, filter) {
			return true
		}
	}

	return false
}

func (c *cmdImageAliasList) imageAliasNameColumnData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Name
}

func (c *cmdImageAliasList) targetColumnData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Target[0:12]
}

func (c *cmdImageAliasList) targetFullColumnData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Target
}

func (c *cmdImageAliasList) typeColumnData(imageAlias api.ImageAliasesEntry) string {
	return strings.ToUpper(imageAlias.Type)
}

func (c *cmdImageAliasList) descriptionColumntData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Description
}

func (c *cmdImageAliasList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdImageAliasListUsage, cmd, args)
	if err != nil {
		return err
	}

	remoteName := c.global.conf.DefaultRemote
	if !parsed[0].Skipped {
		remoteName = parsed[0].List[0].String
	}

	filters := parsed[1].StringList

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// List the aliases
	allAliases, err := remoteServer.GetImageAliases()
	if err != nil {
		return err
	}

	aliases := []api.ImageAliasesEntry{}
	for _, alias := range allAliases {
		if !c.aliasShouldShow(filters, &alias) {
			continue
		}

		if alias.Type == "" {
			alias.Type = "container"
		}

		aliases = append(aliases, alias)
	}

	if len(aliases) == 0 {
		u.HintMissingSpace(args, i18n.G("No matching alias found"))
	}

	return c.lister.run(aliases)
}

// Rename.
type cmdImageAliasRename struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

var cmdImageAliasRenameUsage = u.Usage{u.Alias.Remote(), u.NewName(u.Alias)}

func (c *cmdImageAliasRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdImageAliasRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename aliases`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageAliasRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdImageAliasRenameUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aliasName := parsed[0].RemoteObject.String
	newAliasName := parsed[1].String

	// Rename the alias
	return d.RenameImageAlias(aliasName, api.ImageAliasesEntryPost{Name: newAliasName})
}
