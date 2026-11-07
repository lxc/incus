package main

import (
	"fmt"

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
	storageVolumeBitmapListCmd := cmdStorageVolumeBitmapList{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c, lister: &listable[api.StorageVolumeBitmap]{global: c.global}}
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
	lister              *listable[api.StorageVolumeBitmap]
}

var cmdStorageVolumeBitmapListUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeBitmapList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageVolumeBitmapListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volume dirty bitmaps")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List storage volume dirty bitmaps"))
	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))

	c.lister.init(cmd, "ncgrbpi", map[rune]listColumn[api.StorageVolumeBitmap]{
		'n': {i18n.G("NAME"), i18n.G("Name"), func(v api.StorageVolumeBitmap) string { return v.Name }},
		'c': {i18n.G("DIRTY BYTES"), i18n.G("Number of dirty bytes"), func(v api.StorageVolumeBitmap) string { return fmt.Sprintf("%d", v.Count) }},
		'g': {i18n.G("GRANULARITY"), i18n.G("Granularity"), func(v api.StorageVolumeBitmap) string { return fmt.Sprintf("%d", v.Granularity) }},
		'r': {i18n.G("RECORDING"), i18n.G("Whether the bitmap is currently tracking data"), func(v api.StorageVolumeBitmap) string { return yesNo(v.Recording) }},
		'b': {i18n.G("BUSY"), i18n.G("Whether the bitmap is busy"), func(v api.StorageVolumeBitmap) string { return yesNo(v.Busy) }},
		'p': {i18n.G("PERSISTENT"), i18n.G("Whether the bitmap is persistent"), func(v api.StorageVolumeBitmap) string { return yesNo(v.Persistent) }},
		'i': {i18n.G("INCONSISTENT"), i18n.G("Whether the bitmap is inconsistent"), func(v api.StorageVolumeBitmap) string { return yesNo(v.Inconsistent) }},
	})

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

	return c.lister.run(bitmaps)
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
