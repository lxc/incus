package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/termios"
)

type cmdStorageVolumeSnapshot struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeSnapshot) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("snapshot")
	cmd.Short = i18n.G("Manage storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage storage volume snapshots`))

	// Create
	storageVolumeSnapshotCreateCmd := cmdStorageVolumeSnapshotCreate{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
	cmd.AddCommand(storageVolumeSnapshotCreateCmd.command())

	// Delete
	storageVolumeSnapshotDeleteCmd := cmdStorageVolumeSnapshotDelete{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
	cmd.AddCommand(storageVolumeSnapshotDeleteCmd.command())

	// List
	storageVolumeSnapshotListCmd := cmdStorageVolumeSnapshotList{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c, lister: &listable[api.StorageVolumeSnapshot]{global: c.global}}
	cmd.AddCommand(storageVolumeSnapshotListCmd.command())

	// Rename
	storageVolumeSnapshotRenameCmd := cmdStorageVolumeSnapshotRename{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
	cmd.AddCommand(storageVolumeSnapshotRenameCmd.command())

	// Restore
	storageVolumeSnapshotRestoreCmd := cmdStorageVolumeSnapshotRestore{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
	cmd.AddCommand(storageVolumeSnapshotRestoreCmd.command())

	// Restore
	storageVolumeSnapshotShowCmd := cmdStorageVolumeSnapshotShow{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
	cmd.AddCommand(storageVolumeSnapshotShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }

	return cmd
}

// Snapshot create.
type cmdStorageVolumeSnapshotCreate struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot

	flagNoExpiry    bool
	flagExpiry      string
	flagReuse       bool
	flagDescription string
}

var cmdStorageVolumeSnapshotCreateUsage = u.Usage{u.Pool.Remote(), u.Volume, u.NewName(u.Snapshot).Optional()}

func (c *cmdStorageVolumeSnapshotCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageVolumeSnapshotCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Snapshot storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Snapshot storage volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume snapshot create default foo snap0
    Create a snapshot of "foo" in pool "default" called "snap0"

incus storage volume snapshot create default vol1 snap0 < config.yaml
    Create a snapshot of "foo" in pool "default" called "snap0" with the configuration from "config.yaml"`))

	cli.AddStringFlag(cmd.Flags(), &c.flagExpiry, "expiry", "", "", i18n.G("Expiry for the new snapshot (either a time span like `1d 3H` or a date in `2006/01/02 15:04 MST` format)"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagNoExpiry, "no-expiry", i18n.G("Ignore any configured auto-expiry for the storage volume"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagReuse, "reuse", i18n.G("If the snapshot name already exists, delete and create a new one"))
	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))
	cli.AddStringFlag(cmd.Flags(), &c.flagDescription, "description", "", "", i18n.G("Snapshot description"))

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

func (c *cmdStorageVolumeSnapshotCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	hasSnapName := !parsed[2].Skipped
	snapName := parsed[2].String

	if c.flagNoExpiry && c.flagExpiry != "" {
		return errors.New(i18n.G("Can't use both --no-expiry and --expiry"))
	}

	// If stdin isn't a terminal, read text from it
	var stdinData api.StorageVolumeSnapshotPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		err = loader.Load(&stdinData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Check if the requested storage volume actually exists
	_, _, err = d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return err
	}

	req := api.StorageVolumeSnapshotsPost{
		Name: snapName,
	}

	if c.flagNoExpiry {
		req.ExpiresAt = &time.Time{}
	} else if c.flagExpiry != "" {
		// Try to parse as a duration.
		expiry, err := instance.GetExpiry(time.Now(), c.flagExpiry)
		if err != nil {
			if !errors.Is(err, instance.ErrInvalidExpiry) {
				return err
			}

			// Fallback to date parsing.
			expiry, err = time.Parse(dateLayout, c.flagExpiry)
			if err != nil {
				return err
			}
		}

		req.ExpiresAt = &expiry
	} else if stdinData.ExpiresAt != nil && !stdinData.ExpiresAt.IsZero() {
		req.ExpiresAt = stdinData.ExpiresAt
	}

	if c.flagReuse && hasSnapName {
		snap, _, _ := d.GetStoragePoolVolumeSnapshot(poolName, "custom", volName, snapName)
		if snap != nil {
			op, err := d.DeleteStoragePoolVolumeSnapshot(poolName, "custom", volName, snapName)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	op, err := d.CreateStoragePoolVolumeSnapshot(poolName, "custom", volName, req)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	opInfo := op.Get()

	snapshots, ok := opInfo.Resources["storage_volume_snapshots"]
	if !ok || len(snapshots) == 0 {
		return errors.New(i18n.G("Didn't get name of new volume snapshot from the server"))
	}

	if len(snapshots) == 1 && !hasSnapName {
		uri, err := url.Parse(snapshots[0])
		if err != nil {
			return err
		}

		fmt.Printf(i18n.G("Volume snapshot name is: %s")+"\n", path.Base(uri.Path))
	}

	return nil
}

// Snapshot delete.
type cmdStorageVolumeSnapshotDelete struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot
}

var cmdStorageVolumeSnapshotDeleteUsage = u.Usage{u.Pool.Remote(), u.Volume, u.Snapshot}

func (c *cmdStorageVolumeSnapshotDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeSnapshotDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete storage volume snapshots`))

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
			return c.global.cmpStoragePoolVolumeSnapshots(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeSnapshotDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	snapName := parsed[2].String

	// If a target was specified, delete the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Delete the snapshot
	op, err := d.DeleteStoragePoolVolumeSnapshot(poolName, "custom", volName, snapName)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage volume snapshot %s deleted from %s")+"\n", snapName, volName)
	}

	return nil
}

// Snapshot list.
type cmdStorageVolumeSnapshotList struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot
	lister                *listable[api.StorageVolumeSnapshot]
}

var cmdStorageVolumeSnapshotListUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeSnapshotList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageVolumeSnapshotListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List storage volume snapshots"))

	c.lister.init(cmd, "nTE", map[rune]listColumn[api.StorageVolumeSnapshot]{
		'n': {i18n.G("NAME"), i18n.G("Name"), c.nameColumnData},
		'T': {i18n.G("TAKEN"), i18n.G("Creation date"), c.takenAtColumnData},
		'E': {i18n.G("EXPIRES"), i18n.G("Expiration date"), c.expiresAtColumnData},
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

func (c *cmdStorageVolumeSnapshotList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// Check if the requested storage volume actually exists
	_, _, err = d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		return err
	}

	snapshots, err := d.GetStoragePoolVolumeSnapshots(poolName, volType, volName)
	if err != nil {
		return err
	}

	return c.lister.run(snapshots)
}

func (c *cmdStorageVolumeSnapshotList) nameColumnData(snapshot api.StorageVolumeSnapshot) string {
	_, snapName, _ := api.GetParentAndSnapshotName(snapshot.Name)
	return snapName
}

func (c *cmdStorageVolumeSnapshotList) takenAtColumnData(snapshot api.StorageVolumeSnapshot) string {
	if snapshot.CreatedAt.IsZero() {
		return " "
	}

	return snapshot.CreatedAt.Local().Format(dateLayout)
}

func (c *cmdStorageVolumeSnapshotList) expiresAtColumnData(snapshot api.StorageVolumeSnapshot) string {
	if snapshot.ExpiresAt == nil || snapshot.ExpiresAt.IsZero() {
		return " "
	}

	return snapshot.ExpiresAt.Local().Format(dateLayout)
}

// Snapshot rename.
type cmdStorageVolumeSnapshotRename struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot
}

var cmdStorageVolumeSnapshotRenameUsage = u.Usage{u.Pool.Remote(), u.Volume, u.Snapshot, u.NewName(u.Snapshot)}

func (c *cmdStorageVolumeSnapshotRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdStorageVolumeSnapshotRenameUsage...)
	cmd.Short = i18n.G("Rename storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename storage volume snapshots`))

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
			return c.global.cmpStoragePoolVolumeSnapshots(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeSnapshotRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotRenameUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	snapName := parsed[2].String
	newSnapName := parsed[3].String

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	op, err := d.RenameStoragePoolVolumeSnapshot(poolName, "custom", volName, snapName, api.StorageVolumeSnapshotPost{Name: newSnapName})
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G(`Renamed storage volume snapshot from "%s" to "%s"`)+"\n", snapName, newSnapName)
	return nil
}

// Snapshot restore.
type cmdStorageVolumeSnapshotRestore struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot
}

var cmdStorageVolumeSnapshotRestoreUsage = u.Usage{u.Pool.Remote(), u.Volume, u.Snapshot}

func (c *cmdStorageVolumeSnapshotRestore) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("restore", cmdStorageVolumeSnapshotRestoreUsage...)
	cmd.Short = i18n.G("Restore storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Restore storage volume snapshots`))
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
			return c.global.cmpStoragePoolVolumeSnapshots(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeSnapshotRestore) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotRestoreUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	snapName := parsed[2].String

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Check if the requested storage volume actually exists
	_, etag, err := d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return err
	}

	return d.UpdateStoragePoolVolume(poolName, "custom", volName, api.StorageVolumePut{Restore: snapName}, etag)
}

// Snapshot show.
type cmdStorageVolumeSnapshotShow struct {
	global                *cmdGlobal
	storage               *cmdStorage
	storageVolume         *cmdStorageVolume
	storageVolumeSnapshot *cmdStorageVolumeSnapshot
}

var cmdStorageVolumeSnapshotShowUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume), u.Snapshot}

func (c *cmdStorageVolumeSnapshotShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageVolumeSnapshotShowUsage...)
	cmd.Short = i18n.G("Show storage volume snapshot configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show storage volume snapshhot configurations`,
	))
	cli.AddStringFlag(cmd.Flags(), &c.storage.flagTarget, "target", "", "", i18n.G("Cluster member name"))

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

func (c *cmdStorageVolumeSnapshotShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeSnapshotShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	snapName := parsed[2].String

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry
	vol, _, err := d.GetStoragePoolVolumeSnapshot(poolName, volType, volName, snapName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&vol, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
