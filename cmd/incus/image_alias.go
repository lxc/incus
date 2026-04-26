package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type imageAliasColumns struct {
	Name string
	Data func(api.ImageAliasesEntry) string
}

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
	imageAliasListCmd := cmdImageAliasList{global: c.global, image: c.image, imageAlias: c}
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
		`Create aliases for existing images`))

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Image alias description")+"``")

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
	parsed, err := cmdImageAliasCreateUsage.Parse(c.global.conf, cmd, args)
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
	parsed, err := cmdImageAliasDeleteUsage.Parse(c.global.conf, cmd, args)
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

	flagFormat  string
	flagColumns string
}

var cmdImageAliasListUsage = u.Usage{u.Colon(u.Remote).Optional(), u.Filter.List(0)}

func (c *cmdImageAliasList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdImageAliasListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List image aliases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List image aliases

Filters may be part of the image hash or part of the image alias name.
Default column layout: aftd

== Columns ==
The -c option takes a comma separated list of arguments that control
which attributes of image aliases to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  a - Alias
  f - Fingerprint
  t - Type
  d - Description`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultImageAliasColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, true)
	}

	return cmd
}

const defaultImageAliasColumns = "aftd"

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

func (c *cmdImageAliasList) parseColumns() ([]imageAliasColumns, error) {
	columnsShorthandMap := map[rune]imageAliasColumns{
		'a': {i18n.G("ALIAS"), c.imageAliasNameColumnData},
		'f': {i18n.G("FINGERPRINT"), c.targetColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumntData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []imageAliasColumns{}

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

func (c *cmdImageAliasList) imageAliasNameColumnData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Name
}

func (c *cmdImageAliasList) targetColumnData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Target[0:12]
}

func (c *cmdImageAliasList) typeColumnData(imageAlias api.ImageAliasesEntry) string {
	return strings.ToUpper(imageAlias.Type)
}

func (c *cmdImageAliasList) descriptionColumntData(imageAlias api.ImageAliasesEntry) string {
	return imageAlias.Description
}

func (c *cmdImageAliasList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdImageAliasListUsage.Parse(c.global.conf, cmd, args)
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
	aliases, err := remoteServer.GetImageAliases()
	if err != nil {
		return err
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, alias := range aliases {
		if !c.aliasShouldShow(filters, &alias) {
			continue
		}

		if alias.Type == "" {
			alias.Type = "container"
		}

		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(alias))
		}

		data = append(data, line)
	}

	sort.Sort(cli.StringList(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, aliases)
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
	parsed, err := cmdImageAliasRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	aliasName := parsed[0].RemoteObject.String
	newAliasName := parsed[1].String

	// Rename the alias
	return d.RenameImageAlias(aliasName, api.ImageAliasesEntryPost{Name: newAliasName})
}
