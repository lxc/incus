package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

type imageAliasColumns struct {
	Name string
	Data func(api.ImageAliasesEntry) string
}

type cmdImageAlias struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageAlias) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("alias")
	cmd.Short = i18n.G("Manage image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage image aliases`))

	// Create
	imageAliasCreateCmd := cmdImageAliasCreate{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasCreateCmd.Command())

	// Delete
	imageAliasDeleteCmd := cmdImageAliasDelete{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasDeleteCmd.Command())

	// List
	imageAliasListCmd := cmdImageAliasList{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasListCmd.Command())

	// Rename
	imageAliasRenameCmd := cmdImageAliasRename{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasRenameCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdImageAliasCreate struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<alias> <fingerprint>"))
	cmd.Short = i18n.G("Create aliases for existing images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create aliases for existing images`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdImageAliasCreate) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Create the alias
	alias := api.ImageAliasesPost{}
	alias.Name = resource.name
	alias.Target = args[1]

	return resource.server.CreateImageAlias(alias)
}

// Delete.
type cmdImageAliasDelete struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<alias>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete image aliases`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageAliasDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Delete the alias
	return resource.server.DeleteImageAlias(resource.name)
}

// List.
type cmdImageAliasList struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias

	flagFormat  string
	flagColumns string
}

func (c *cmdImageAliasList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:] [<filters>...]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List image aliases

Filters may be part of the image hash or part of the image alias name.
Default column layout: aftd

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  a - Alias
  f - Fingerprint
  t - Type
  d - Description`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultImageAliasColumns, i18n.G("Columns")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdImageAliasList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, name, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Process the filters
	filters := []string{}
	if name != "" {
		filters = append(filters, name)
	}

	if len(args) > 1 {
		filters = append(filters, args[1:]...)
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

	return cli.RenderTable(c.flagFormat, header, data, aliases)
}

// Rename.
type cmdImageAliasRename struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<alias> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename aliases`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageAliasRename) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Rename the alias
	return resource.server.RenameImageAlias(resource.name, api.ImageAliasesEntryPost{Name: args[1]})
}
