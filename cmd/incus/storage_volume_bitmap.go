package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdStorageVolumeBitmap struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeBitmap) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("bitmap")
	cmd.Short = i18n.G("Manage storage volume dirty bitmaps")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage storage volume dirty bitmaps`))

	// Create
	storageVolumeBitmapCreateCmd := cmdStorageVolumeBitmapCreate{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapCreateCmd.command())

	// Delete
	storageVolumeBitmapDeleteCmd := cmdStorageVolumeBitmapDelete{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapDeleteCmd.command())

	// List
	storageVolumeBitmapListCmd := cmdStorageVolumeBitmapList{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapListCmd.command())

	// Show
	storageVolumeBitmapShowCmd := cmdStorageVolumeBitmapShow{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }

	return cmd
}

// Bitmap create.
type cmdStorageVolumeBitmapCreate struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap

	flagGranularity int
	flagPersistent  bool
	flagDisabled    bool
}

var cmdStorageVolumeBitmapCreateUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume), u.NewName(u.Placeholder(i18n.G("bitmap")))}

func (c *cmdStorageVolumeBitmapCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageVolumeBitmapCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create a dirty bitmap on a storage volume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create a dirty bitmap on a storage volume`))

	cmd.RunE = c.run
	cli.AddIntFlag(cmd.Flags(), &c.flagGranularity, "granularity", i18n.G("Granularity of the dirty bitmap in bytes"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagPersistent, "persistent", i18n.G("Store the bitmap on disk"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagDisabled, "disabled", i18n.G("Create the bitmap in the disabled state"))
	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeBitmapCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// If a target was specified, create the bitmap on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	bitmap := api.StorageVolumeBitmapsPost{
		Name:        parsed[2].String,
		Granularity: c.flagGranularity,
		Persistent:  c.flagPersistent,
		Disabled:    c.flagDisabled,
	}

	err = d.CreateStorageVolumeBitmap(poolName, volType, volName, bitmap)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to create bitmap: %w"), err)
	}

	return nil
}

// Bitmap delete.
type cmdStorageVolumeBitmapDelete struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap
}

var cmdStorageVolumeBitmapDeleteUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume), u.Placeholder(i18n.G("bitmap"))}

func (c *cmdStorageVolumeBitmapDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeBitmapDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete storage volume dirty bitmaps")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete storage volume dirty bitmaps`))

	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeBitmaps(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeBitmapDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	bitmapName := parsed[2].String

	// If a target was specified, delete the bitmap on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	err = d.DeleteStorageVolumeBitmap(poolName, volType, volName, bitmapName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to delete bitmap: %w"), err)
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Bitmap %s deleted from volume %s")+"\n", bitmapName, volName)
	}

	return nil
}

// Bitmap list.
type cmdStorageVolumeBitmapList struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap

	flagFormat  string
	flagColumns string
}

var cmdStorageVolumeBitmapListUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

const defaultStorageVolumeBitmapColumns = "ncgrbpi"

func (c *cmdStorageVolumeBitmapList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageVolumeBitmapListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volume dirty bitmaps")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List storage volume dirty bitmaps

	The -c option takes a (optionally comma-separated) list of arguments
	that control which bitmap attributes to output when displaying in
	table or csv format.

	Column shorthand chars:
		n - Name
		c - Number of dirty bytes
		g - Granularity
		r - Recording
		b - Busy
		p - Persistent
		i - Inconsistent`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagColumns, "columns|c", defaultStorageVolumeBitmapColumns, "", i18n.G("Columns"))
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", c.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`))
	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

type storageVolumeBitmapColumn struct {
	Name string
	Data func(api.StorageVolumeBitmap) string
}

func boolYesNo(value bool) string {
	if value {
		return i18n.G("YES")
	}

	return i18n.G("NO")
}

func (c *cmdStorageVolumeBitmapList) parseColumns() ([]storageVolumeBitmapColumn, error) {
	columnsShorthandMap := map[rune]storageVolumeBitmapColumn{
		'n': {i18n.G("NAME"), func(v api.StorageVolumeBitmap) string { return v.Name }},
		'c': {i18n.G("DIRTY BYTES"), func(v api.StorageVolumeBitmap) string { return fmt.Sprintf("%d", v.Count) }},
		'g': {i18n.G("GRANULARITY"), func(v api.StorageVolumeBitmap) string { return fmt.Sprintf("%d", v.Granularity) }},
		'r': {i18n.G("RECORDING"), func(v api.StorageVolumeBitmap) string { return boolYesNo(v.Recording) }},
		'b': {i18n.G("BUSY"), func(v api.StorageVolumeBitmap) string { return boolYesNo(v.Busy) }},
		'p': {i18n.G("PERSISTENT"), func(v api.StorageVolumeBitmap) string { return boolYesNo(v.Persistent) }},
		'i': {i18n.G("INCONSISTENT"), func(v api.StorageVolumeBitmap) string { return boolYesNo(v.Inconsistent) }},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []storageVolumeBitmapColumn{}

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

func (c *cmdStorageVolumeBitmapList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeBitmapListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// If a target was specified, list the bitmaps on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	bitmaps, err := d.GetStorageVolumeBitmaps(poolName, volType, volName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to list bitmaps: %w"), err)
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, bitmap := range bitmaps {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(bitmap))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, bitmaps)
}

// Bitmap show.
type cmdStorageVolumeBitmapShow struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap
}

var cmdStorageVolumeBitmapShowUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume), u.Placeholder(i18n.G("bitmap"))}

func (c *cmdStorageVolumeBitmapShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageVolumeBitmapShowUsage...)
	cmd.Short = i18n.G("Show storage volume dirty bitmap information")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show storage volume dirty bitmap information`))

	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeBitmaps(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeBitmapShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	bitmapName := parsed[2].String

	// If a target was specified, show the bitmap on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	bitmap, err := d.GetStorageVolumeBitmap(poolName, volType, volName, bitmapName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to get bitmap: %w"), err)
	}

	data, err := yaml.Dump(bitmap, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
