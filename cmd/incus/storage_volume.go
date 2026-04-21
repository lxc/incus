package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	internalIO "github.com/lxc/incus/v6/internal/io"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

type volumeColumn struct {
	Name       string
	Data       func(api.StorageVolume, api.StorageVolumeState) string
	NeedsState bool
}

type cmdStorageVolume struct {
	global                *cmdGlobal
	storage               *cmdStorage
	flagDestinationTarget string
}

func parseVolume(defaultType string, name string) (string, string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], defaultType
	} else if len(fields) == 2 && !slices.Contains([]string{"custom", "image", "container", "virtual-machine"}, fields[0]) {
		return name, defaultType
	}

	return fields[1], fields[0]
}

func (c *cmdStorageVolume) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("volume")
	cmd.Short = i18n.G("Manage storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Manage storage volumes

Unless specified through a prefix, all volume operations affect "custom" (user created) volumes.`))

	// Attach
	storageVolumeAttachCmd := cmdStorageVolumeAttach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachCmd.command())

	// Attach profile
	storageVolumeAttachProfileCmd := cmdStorageVolumeAttachProfile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeAttachProfileCmd.command())

	// Copy
	storageVolumeCopyCmd := cmdStorageVolumeCopy{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCopyCmd.command())

	// Create
	storageVolumeCreateCmd := cmdStorageVolumeCreate{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeCreateCmd.command())

	// Delete
	storageVolumeDeleteCmd := cmdStorageVolumeDelete{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDeleteCmd.command())

	// Detach
	storageVolumeDetachCmd := cmdStorageVolumeDetach{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeDetachCmd.command())

	// Detach profile
	storageVolumeDetachProfileCmd := cmdStorageVolumeDetachProfile{global: c.global, storage: c.storage, storageVolume: c, storageVolumeDetach: &storageVolumeDetachCmd}
	cmd.AddCommand(storageVolumeDetachProfileCmd.command())

	// Edit
	storageVolumeEditCmd := cmdStorageVolumeEdit{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeEditCmd.command())

	// Export
	storageVolumeExportCmd := cmdStorageVolumeExport{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeExportCmd.command())

	// Get
	storageVolumeGetCmd := cmdStorageVolumeGet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeGetCmd.command())

	// Import
	storageVolumeImportCmd := cmdStorageVolumeImport{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeImportCmd.command())

	// Info
	storageVolumeInfoCmd := cmdStorageVolumeInfo{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeInfoCmd.command())

	// List
	storageVolumeListCmd := cmdStorageVolumeList{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeListCmd.command())

	// NBD
	storageVolumeNBDCmd := cmdStorageVolumeNBD{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeNBDCmd.Command())

	// Rename
	storageVolumeRenameCmd := cmdStorageVolumeRename{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeRenameCmd.command())

	// Move
	storageVolumeMoveCmd := cmdStorageVolumeMove{global: c.global, storage: c.storage, storageVolume: c, storageVolumeCopy: &storageVolumeCopyCmd, storageVolumeRename: &storageVolumeRenameCmd}
	cmd.AddCommand(storageVolumeMoveCmd.command())

	// Set
	storageVolumeSetCmd := cmdStorageVolumeSet{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSetCmd.command())

	// Show
	storageVolumeShowCmd := cmdStorageVolumeShow{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeShowCmd.command())

	// Snapshot
	storageVolumeSnapshotCmd := cmdStorageVolumeSnapshot{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeSnapshotCmd.command())

	// Unset
	storageVolumeUnsetCmd := cmdStorageVolumeUnset{global: c.global, storage: c.storage, storageVolume: c, storageVolumeSet: &storageVolumeSetCmd}
	cmd.AddCommand(storageVolumeUnsetCmd.command())

	// File
	storageVolumeFileCmd := cmdStorageVolumeFile{global: c.global, storage: c.storage, storageVolume: c}
	cmd.AddCommand(storageVolumeFileCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdStorageVolume) parseVolumeWithPool(name string) (string, string) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], ""
	}

	return fields[1], fields[0]
}

// Attach.
type cmdStorageVolumeAttach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeAttachUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Instance, u.NewName(u.Device).Optional(u.Path.Optional())}

func (c *cmdStorageVolumeAttach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("attach", cmdStorageVolumeAttachUsage...)
	cmd.Short = i18n.G("Attach new custom storage volumes to instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Attach new custom storage volumes to instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpInstanceNamesFromRemote(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeAttach) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeAttachUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	instanceName := parsed[2].String
	devName := volName
	devPath := ""
	if !parsed[3].Skipped {
		devName = parsed[3].List[0].String
		devPath = parsed[3].List[1].String
	}

	// Prepare the instance's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   poolName,
		"source": volName,
		"path":   devPath,
	}

	// Add the device to the instance
	err = instanceDeviceAdd(d, instanceName, devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile.
type cmdStorageVolumeAttachProfile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeAttachProfileUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Profile, u.NewName(u.Device).Optional(u.Path.Optional())}

func (c *cmdStorageVolumeAttachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("attach-profile", cmdStorageVolumeAttachProfileUsage...)
	cmd.Short = i18n.G("Attach new custom storage volumes to profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Attach new custom storage volumes to profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpProfileNamesFromRemote(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeAttachProfile) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeAttachProfileUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	profileName := parsed[2].String
	devName := volName
	devPath := ""
	if !parsed[3].Skipped {
		devName = parsed[3].List[0].String
		devPath = parsed[3].List[1].String
	}

	// Check if the requested storage volume actually exists
	vol, _, err := d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return err
	}

	// Prepare the instance's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   poolName,
		"source": vol.Name,
	}

	// Ignore path for block volumes
	if vol.ContentType != "block" {
		device["path"] = devPath
	}

	// Add the device to the instance
	err = profileDeviceAdd(d, profileName, devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Copy.
type cmdStorageVolumeCopy struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagMode                string
	flagVolumeOnly          bool
	flagTargetProject       string
	flagRefresh             bool
	flagRefreshExcludeOlder bool
}

var cmdStorageVolumeCopyUsage = u.Usage{u.MakePath(u.Pool, u.Volume, u.Snapshot.Optional()).Remote(), u.MakePath(u.Pool, u.NewName(u.Volume)).Remote()}

func (c *cmdStorageVolumeCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("copy", cmdStorageVolumeCopyUsage...)
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Copy custom storage volumes`))

	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay.")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolume.flagDestinationTarget, "destination-target", "", i18n.G("Destination cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagVolumeOnly, "volume-only", false, i18n.G("Copy the volume without its snapshots"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Refresh and update the existing storage volume copies"))
	cmd.Flags().BoolVar(&c.flagRefreshExcludeOlder, "refresh-exclude-older", false, i18n.G("During refresh, exclude source snapshots earlier than latest target snapshot"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePoolWithVolume(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// copyOrMove runs the post-parsing command logic.
func (c *cmdStorageVolumeCopy) copyOrMove(cmd *cobra.Command, parsed []*u.Parsed) error {
	srcServer := parsed[0].RemoteServer
	srcPoolName := parsed[0].RemoteObject.List[0].String
	srcVolName := parsed[0].RemoteObject.List[1].String

	// This function can be called from both the `copy` and `move` commands. As their first arguments
	// have a different grammar, additional care is taken here to normalize them.
	srcIsSnapshot := false
	srcSnapName := ""
	if cmd.Name() == "copy" {
		srcIsSnapshot = !parsed[0].RemoteObject.List[2].Skipped
		srcSnapName = parsed[0].RemoteObject.List[2].String
	}

	dstServer := parsed[1].RemoteServer
	dstPoolName := parsed[1].RemoteObject.List[0].String
	dstVolName := parsed[1].RemoteObject.List[1].String

	// If the source server is standalone then --target cannot be provided.
	if c.storage.flagTarget != "" && !srcServer.IsClustered() {
		return errors.New(i18n.G("Cannot set --target when source server is not clustered"))
	}

	if c.storage.flagTarget != "" {
		srcServer = srcServer.UseTarget(c.storage.flagTarget)
	}

	// Check if requested storage volume exists.
	srcVol, _, err := srcServer.GetStoragePoolVolume(srcPoolName, "custom", srcVolName)
	if err != nil {
		return err
	}

	if srcIsSnapshot && c.flagVolumeOnly {
		return errors.New(i18n.G("Cannot set --volume-only when copying a snapshot"))
	}

	// If the volume is in local storage, set the target to its location (or provide a helpful error
	// message if the target is incorrect). If the volume is in remote storage (and the source server is clustered) we
	// can use any provided target. Note that for standalone servers, this will set the target to "none".
	if srcVol.Location != "" && srcVol.Location != "none" {
		if c.storage.flagTarget != "" && c.storage.flagTarget != srcVol.Location {
			return fmt.Errorf(i18n.G("Given target %q does not match source volume location %q"), c.storage.flagTarget, srcVol.Location)
		}

		srcServer = srcServer.UseTarget(srcVol.Location)
	} else if c.storage.flagTarget != "" && srcServer.IsClustered() {
		srcServer = srcServer.UseTarget(c.storage.flagTarget)
	}

	// We can always set the destination target if the destination server is clustered (for local storage volumes this
	// places the volume on the target member, for remote volumes this does nothing).
	if c.storageVolume.flagDestinationTarget != "" {
		if !dstServer.IsClustered() {
			return errors.New(i18n.G("Cannot set --destination-target when destination server is not clustered"))
		}

		dstServer = dstServer.UseTarget(c.storageVolume.flagDestinationTarget)
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	var op incus.RemoteOperation

	// Messages
	opMsg := i18n.G("Copying the storage volume: %s")
	finalMsg := i18n.G("Storage volume copied successfully!")

	if cmd.Name() == "move" {
		opMsg = i18n.G("Moving the storage volume: %s")
		finalMsg = i18n.G("Storage volume moved successfully!")
	}

	// If source is a snapshot get source snapshot volume info and apply to the srcVol.
	if srcIsSnapshot {
		srcVolSnapshot, _, err := srcServer.GetStoragePoolVolumeSnapshot(srcPoolName, "custom", srcVolName, srcSnapName)
		if err != nil {
			return err
		}

		// Copy info from source snapshot into source volume used for new volume.
		srcVol.Name = srcVolName + "/" + srcSnapName
		srcVol.Config = srcVolSnapshot.Config
		srcVol.Description = srcVolSnapshot.Description
	}

	if cmd.Name() == "move" && srcServer == dstServer {
		args := &incus.StoragePoolVolumeMoveArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = false
		args.Project = c.flagTargetProject

		op, err = dstServer.MoveStoragePoolVolume(dstPoolName, srcServer, srcPoolName, *srcVol, args)
		if err != nil {
			return err
		}
	} else {
		args := &incus.StoragePoolVolumeCopyArgs{}
		args.Name = dstVolName
		args.Mode = mode
		args.VolumeOnly = c.flagVolumeOnly
		args.Refresh = c.flagRefresh
		args.RefreshExcludeOlder = c.flagRefreshExcludeOlder

		if c.flagTargetProject != "" {
			dstServer = dstServer.UseProject(c.flagTargetProject)
		}

		op, err = dstServer.CopyStoragePoolVolume(dstPoolName, srcServer, srcPoolName, *srcVol, args)
		if err != nil {
			return err
		}
	}

	// Register progress handler
	progress := cli.ProgressRenderer{
		Format: opMsg,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	if cmd.Name() == "move" && srcServer != dstServer {
		err = srcServer.DeleteStoragePoolVolume(srcPoolName, srcVol.Type, srcVolName)
		if err != nil {
			progress.Done("")
			return fmt.Errorf(i18n.G("Failed deleting source volume after copy: %w"), err)
		}
	}

	progress.Done(finalMsg)

	return nil
}

func (c *cmdStorageVolumeCopy) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeCopyUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.copyOrMove(cmd, parsed)
}

// Create.
type cmdStorageVolumeCreate struct {
	global          *cmdGlobal
	storage         *cmdStorage
	storageVolume   *cmdStorageVolume
	flagContentType string
	flagDescription string
}

var cmdStorageVolumeCreateUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.NewName(u.Volume)), u.KV.List(0)}

func (c *cmdStorageVolumeCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageVolumeCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create new custom storage volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume create default foo
    Create custom storage volume "foo" in pool "default"

incus storage volume create default foo < config.yaml
    Create custom storage volume "foo" in pool "default" with configuration from config.yaml`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagContentType, "type", "filesystem", i18n.G("Content type, block or filesystem")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Volume description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	var volumePut api.StorageVolumePut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin, yaml.WithKnownFields())
		if err != nil {
			return err
		}

		err = loader.Load(&volumePut)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	// Create the storage volume entry
	vol := api.StorageVolumesPost{
		Name:             volName,
		Type:             "custom",
		ContentType:      c.flagContentType,
		StorageVolumePut: volumePut,
	}

	if volumePut.Config == nil {
		vol.Config = map[string]string{}
	}

	maps.Copy(vol.Config, keys)

	if c.flagDescription != "" {
		vol.Description = c.flagDescription
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	err = d.CreateStoragePoolVolume(poolName, vol)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage volume %s created")+"\n", volName)
	}

	return nil
}

// Delete.
type cmdStorageVolumeDelete struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeDeleteUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete custom storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

func (c *cmdStorageVolumeDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// If a target was specified, delete the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Delete the volume
	err = d.DeleteStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage volume %s deleted")+"\n", volName)
	}

	return nil
}

// Detach.
type cmdStorageVolumeDetach struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeDetachUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Instance, u.Device.Optional()}

func (c *cmdStorageVolumeDetach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("detach", cmdStorageVolumeDetachUsage...)
	cmd.Short = i18n.G("Detach custom storage volumes from instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Detach custom storage volumes from instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeInstances(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Find a matching device.
func (c *cmdStorageVolumeDetach) findDevice(devices map[string]map[string]string, poolName string, volName string, dev *u.Parsed) (string, error) {
	hasDevice := !dev.Skipped
	devName := dev.String
	found := false
	for n, d := range devices {
		if hasDevice {
			if n == devName {
				if d["type"] != "disk" {
					return "", fmt.Errorf(i18n.G("The specified device is not a disk (%s device)"), d["type"])
				}

				if d["pool"] != poolName {
					return "", fmt.Errorf(i18n.G("The specified disk is not in the given pool (found %s)"), d["pool"])
				}

				if d["source"] != volName {
					return "", fmt.Errorf(i18n.G("The specified disk does not point to the given storage volume (found %s)"), d["source"])
				}

				found = true
				break
			}

			continue
		}

		if d["type"] == "disk" && d["pool"] == poolName && d["source"] == volName {
			if found {
				return "", errors.New(i18n.G("More than one device matches, specify the device name"))
			}

			devName = n
			found = true
		}
	}

	if !found {
		return "", errors.New(i18n.G("No device found for this storage volume"))
	}

	return devName, nil
}

func (c *cmdStorageVolumeDetach) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeDetachUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	instanceName := parsed[2].String

	// Get the instance entry
	inst, etag, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	devName, err := c.findDevice(inst.Devices, poolName, volName, parsed[3])
	if err != nil {
		return err
	}

	// Remove the device
	delete(inst.Devices, devName)
	op, err := d.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile.
type cmdStorageVolumeDetachProfile struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeDetach *cmdStorageVolumeDetach
}

var cmdStorageVolumeDetachProfileUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Profile, u.Device.Optional()}

func (c *cmdStorageVolumeDetachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("detach-profile", cmdStorageVolumeDetachProfileUsage...)
	cmd.Short = i18n.G("Detach custom storage volumes from profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Detach custom storage volumes from profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeProfiles(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeDetachProfile) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeDetachProfileUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	profileName := parsed[2].String

	// Get the profile entry
	profile, etag, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	devName, err := c.storageVolumeDetach.findDevice(profile.Devices, poolName, volName, parsed[3])
	if err != nil {
		return err
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = d.UpdateProfile(profileName, profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Edit.
type cmdStorageVolumeEdit struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

// The parsing is ambiguous here, so we try to disambiguate by using a set of reserved names.
var cmdStorageVolumeEditUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume, u.Snapshot.Optional())}

func (c *cmdStorageVolumeEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageVolumeEditUsage...)
	cmd.Short = i18n.G("Edit storage volume configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit storage volume configurations as YAML

If the type is not specified, incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume edit default container/c1
    Edit container storage volume "c1" in pool "default"

incus storage volume edit default foo < volume.yaml
    Edit custom storage volume "foo" in pool "default" using the content of volume.yaml`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

func (c *cmdStorageVolumeEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage volume.
### Any line starting with a '# will be ignored.
###
### A storage volume consists of a set of configuration items.
###
### name: foo
### type: custom
### used_by: []
### config:
###   size: "61203283968"`)
}

func (c *cmdStorageVolumeEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	isSnapshot := !parsed[1].List[2].Skipped
	snapName := parsed[1].List[2].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		if isSnapshot {
			newdata := api.StorageVolumeSnapshotPut{}
			err = loader.Load(&newdata)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}

			err := d.UpdateStoragePoolVolumeSnapshot(poolName, volType, volName, snapName, newdata, "")
			if err != nil {
				return err
			}

			return nil
		}

		newdata := api.StorageVolumePut{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateStoragePoolVolume(poolName, volType, volName, newdata, "")
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	var data []byte
	var snapVol *api.StorageVolumeSnapshot
	var vol *api.StorageVolume
	etag := ""
	if isSnapshot {
		// Extract the current value
		snapVol, etag, err = d.GetStoragePoolVolumeSnapshot(poolName, volType, volName, snapName)
		if err != nil {
			return err
		}

		data, err = yaml.Dump(&snapVol, yaml.V2)
		if err != nil {
			return err
		}
	} else {
		// Extract the current value
		vol, etag, err = d.GetStoragePoolVolume(poolName, volType, volName)
		if err != nil {
			return err
		}

		data, err = yaml.Dump(&vol, yaml.V2)
		if err != nil {
			return err
		}
	}

	// Spawn the editor
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	if isSnapshot {
		for {
			// Parse the text received from the editor
			newdata := api.StorageVolumeSnapshotPut{}
			err = yaml.Load(content, &newdata)
			if err == nil {
				err = d.UpdateStoragePoolVolumeSnapshot(poolName, volType, volName, snapName, newdata, etag)
			}

			// Respawn the editor
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
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

	for {
		// Parse the text received from the editor
		newdata := api.StorageVolume{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateStoragePoolVolume(poolName, volType, volName, newdata.Writable(), etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
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

// Get.
type cmdStorageVolumeGet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagIsProperty bool
}

var cmdStorageVolumeGetUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume, u.Snapshot.Optional()), u.Key}

func (c *cmdStorageVolumeGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdStorageVolumeGetUsage...)
	cmd.Short = i18n.G("Get values for storage volume configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for storage volume configuration keys

If the type is not specified, incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".

For snapshots, add the snapshot name (only if type is one of custom, container or virtual-machine).`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume get default data size
    Returns the size of a custom volume "data" in pool "default"

incus storage volume get default virtual-machine/data snapshots.expiry
    Returns the snapshot expiration period for a virtual machine "data" in pool "default"`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	isSnapshot := !parsed[1].List[2].Skipped
	snapName := parsed[1].List[2].String
	key := parsed[2].String

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	if isSnapshot {
		resp, _, err := d.GetStoragePoolVolumeSnapshot(poolName, volType, volName, snapName)
		if err != nil {
			return err
		}

		if c.flagIsProperty {
			res, err := getFieldByJSONTag(resp, key)
			if err != nil {
				return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool volume snapshot %s/%s: %v"), key, volName, snapName, err)
			}

			fmt.Printf("%v\n", res)
		} else {
			v, ok := resp.Config[key]
			if ok {
				fmt.Println(v)
			}
		}

		return nil
	}

	// Get the storage volume entry
	resp, _, err := d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		// Give more context on missing volumes.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Storage pool volume \"%s/%s\" not found", volType, volName)
		}

		return err
	}

	if c.flagIsProperty {
		res, err := getFieldByJSONTag(resp, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool volume %q: %v"), key, volName, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[key]
		if ok {
			fmt.Println(v)
		}
	}

	return nil
}

// Info.
type cmdStorageVolumeInfo struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeInfoUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdStorageVolumeInfoUsage...)
	cmd.Short = i18n.G("Show storage volume state information")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show storage volume state information

If the type is not specified, Incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume info default foo
    Returns state information for a custom volume "foo" in pool "default"

incus storage volume info default virtual-machine/v1
    Returns state information for virtual machine "v1" in pool "default"`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

func (c *cmdStorageVolumeInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the data.
	vol, _, err := d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		// Give more context on missing volumes.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Storage pool volume \"%s/%s\" not found", volType, volName)
		}

		return err
	}

	// Instead of failing here if the usage cannot be determined, it is just omitted.
	volState, _ := d.GetStoragePoolVolumeState(poolName, volType, volName)

	volSnapshots, err := d.GetStoragePoolVolumeSnapshots(poolName, volType, volName)
	if err != nil {
		return err
	}

	var volBackups []api.StorageVolumeBackup
	if d.HasExtension("custom_volume_backup") && volType == "custom" {
		volBackups, err = d.GetStorageVolumeBackups(poolName, volName)
		if err != nil {
			return err
		}
	}

	// Render the overview.
	fmt.Printf(i18n.G("Name: %s")+"\n", vol.Name)
	if vol.Description != "" {
		fmt.Printf(i18n.G("Description: %s")+"\n", vol.Description)
	}

	if vol.Type == "" {
		vol.Type = "custom"
	}

	fmt.Printf(i18n.G("Type: %s")+"\n", vol.Type)

	if vol.ContentType == "" {
		vol.ContentType = "filesystem"
	}

	fmt.Printf(i18n.G("Content type: %s")+"\n", vol.ContentType)

	if vol.Location != "" && d.IsClustered() {
		fmt.Printf(i18n.G("Location: %s")+"\n", vol.Location)
	}

	if volState != nil && volState.Usage != nil {
		fmt.Printf(i18n.G("Usage: %s")+"\n", units.GetByteSizeStringIEC(int64(volState.Usage.Used), 2))
		if volState.Usage.Total > 0 {
			fmt.Printf(i18n.G("Total: %s")+"\n", units.GetByteSizeStringIEC(int64(volState.Usage.Total), 2))
		}
	}

	if !vol.CreatedAt.IsZero() {
		fmt.Printf(i18n.G("Created: %s")+"\n", vol.CreatedAt.Local().Format(dateLayout))
	}

	// List snapshots
	firstSnapshot := true
	if len(volSnapshots) > 0 {
		snapData := [][]string{}

		for _, snap := range volSnapshots {
			if firstSnapshot {
				fmt.Println("\n" + i18n.G("Snapshots:"))
			}

			var row []string

			fields := strings.Split(snap.Name, instance.SnapshotDelimiter)
			row = append(row, fields[len(fields)-1])
			row = append(row, snap.Description)

			if snap.ExpiresAt != nil {
				row = append(row, snap.ExpiresAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			firstSnapshot = false
			snapData = append(snapData, row)
		}

		sort.Sort(cli.SortColumnsNaturally(snapData))
		snapHeader := []string{
			i18n.G("Name"),
			i18n.G("Description"),
			i18n.G("Expires at"),
		}

		_ = cli.RenderTable(os.Stdout, cli.TableFormatTable, snapHeader, snapData, volSnapshots)
	}

	// List backups
	firstBackup := true
	if len(volBackups) > 0 {
		backupData := [][]string{}

		for _, backup := range volBackups {
			if firstBackup {
				fmt.Println("\n" + i18n.G("Backups:"))
			}

			var row []string
			row = append(row, backup.Name)

			if !backup.CreatedAt.IsZero() {
				row = append(row, backup.CreatedAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if !backup.ExpiresAt.IsZero() {
				row = append(row, backup.ExpiresAt.Local().Format(dateLayout))
			} else {
				row = append(row, " ")
			}

			if backup.VolumeOnly {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			if backup.OptimizedStorage {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			firstBackup = false
			backupData = append(backupData, row)
		}

		backupHeader := []string{
			i18n.G("Name"),
			i18n.G("Taken at"),
			i18n.G("Expires at"),
			i18n.G("Volume Only"),
			i18n.G("Optimized Storage"),
		}

		_ = cli.RenderTable(os.Stdout, cli.TableFormatTable, backupHeader, backupData, volBackups)
	}

	return nil
}

// List.
type cmdStorageVolumeList struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagFormat      string
	flagColumns     string
	flagAllProjects bool

	defaultColumns string
}

var cmdStorageVolumeListUsage = u.Usage{u.Pool.Remote(), u.Filter.List(0)}

func (c *cmdStorageVolumeList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageVolumeListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volumes")

	c.defaultColumns = "etndcuL"
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", c.defaultColumns, i18n.G("Columns")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("All projects")+"``")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List storage volumes

A single keyword like "vol" which will list any storage volume with a name starting by "vol".
A regular expression on the storage volume name. (e.g. .*vol.*01$).
A key/value pair where the key is a storage volume field name. Multiple values must be delimited by ','.

Examples:
  - "type=custom" will list all custom storage volumes
  - "type=custom content_type=block" will list all custom block storage volumes

== Columns ==
The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Column shorthand chars:
    c - Content type (filesystem or block)
    d - Description
    e - Project name
    L - Location of the instance (e.g. its cluster member)
    n - Name
    t - Type of volume (custom, image, container or virtual-machine)
    u - Number of references (used by)
    U - Current disk usage`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String

	// Process the filters
	filters := []string{}
	for _, filter := range parsed[1].StringList {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + "$"
			}

			filter = fmt.Sprintf("name=(%s|^%s.*)", regexpValue, key)
		}

		filters = append(filters, filter)
	}

	var volumes []api.StorageVolume
	if c.flagAllProjects {
		volumes, err = d.GetStoragePoolVolumesWithFilterAllProjects(poolName, filters)
	} else {
		volumes, err = d.GetStoragePoolVolumesWithFilter(poolName, filters)
	}

	if err != nil {
		return err
	}

	// Process the columns
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, vol := range volumes {
		row := []string{}
		for _, column := range columns {
			if column.NeedsState && !instance.IsSnapshot(vol.Name) && vol.Type != "image" {
				state, err := d.UseProject(vol.Project).GetStoragePoolVolumeState(poolName, vol.Type, vol.Name)
				if err != nil {
					return err
				}

				row = append(row, column.Data(vol, *state))
			} else {
				row = append(row, column.Data(vol, api.StorageVolumeState{}))
			}
		}
		data = append(data, row)
	}

	if len(columns) >= 2 {
		sort.Sort(cli.ByNameAndType(data))
	}

	rawData := make([]*api.StorageVolume, len(volumes))
	for i := range volumes {
		rawData[i] = &volumes[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, headers, data, rawData)
}

func (c *cmdStorageVolumeList) parseColumns(clustered bool) ([]volumeColumn, error) {
	columnsShorthandMap := map[rune]volumeColumn{
		't': {Name: i18n.G("TYPE"), Data: c.typeColumnData},
		'n': {Name: i18n.G("NAME"), Data: c.nameColumnData},
		'd': {Name: i18n.G("DESCRIPTION"), Data: c.descriptionColumnData},
		'c': {Name: i18n.G("CONTENT-TYPE"), Data: c.contentTypeColumnData},
		'u': {Name: i18n.G("USED BY"), Data: c.usedByColumnData},
		'U': {Name: i18n.G("USAGE"), Data: c.usageColumnData, NeedsState: true},
	}

	if clustered {
		columnsShorthandMap['L'] = volumeColumn{Name: i18n.G("LOCATION"), Data: c.locationColumnData}
	} else {
		if c.flagColumns != c.defaultColumns {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, errors.New(i18n.G("Can't specify column L when not clustered"))
			}
		}
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "L", "")
	}

	if c.flagAllProjects {
		columnsShorthandMap['e'] = volumeColumn{Name: i18n.G("PROJECT"), Data: c.projectColumnData}
	} else {
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "e", "")
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []volumeColumn{}
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

func (c *cmdStorageVolumeList) typeColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	if instance.IsSnapshot(vol.Name) {
		return fmt.Sprintf("%s (snapshot)", vol.Type)
	}

	return vol.Type
}

func (c *cmdStorageVolumeList) nameColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	return vol.Name
}

func (c *cmdStorageVolumeList) descriptionColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	return vol.Description
}

func (c *cmdStorageVolumeList) contentTypeColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	if vol.ContentType == "" {
		return "filesystem"
	}

	return vol.ContentType
}

func (c *cmdStorageVolumeList) usedByColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	return strconv.Itoa(len(vol.UsedBy))
}

func (c *cmdStorageVolumeList) locationColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	return vol.Location
}

func (c *cmdStorageVolumeList) usageColumnData(_ api.StorageVolume, state api.StorageVolumeState) string {
	if state.Usage != nil {
		return units.GetByteSizeStringIEC(int64(state.Usage.Used), 2)
	}

	return ""
}

func (c *cmdStorageVolumeList) projectColumnData(vol api.StorageVolume, _ api.StorageVolumeState) string {
	return vol.Project
}

// Move.
type cmdStorageVolumeMove struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeCopy   *cmdStorageVolumeCopy
	storageVolumeRename *cmdStorageVolumeRename
}

var cmdStorageVolumeMoveUsage = u.Usage{u.MakePath(u.Pool, u.Volume).Remote(), u.MakePath(u.Pool, u.NewName(u.Volume)).Remote()}

func (c *cmdStorageVolumeMove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("move", cmdStorageVolumeMoveUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move custom storage volumes between pools")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Move custom storage volumes between pools`))

	cmd.Flags().StringVar(&c.storageVolumeCopy.flagMode, "mode", "pull", i18n.G("Transfer mode, one of pull (default), push or relay")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolume.flagDestinationTarget, "destination-target", "", i18n.G("Destination cluster member name")+"``")
	cmd.Flags().StringVar(&c.storageVolumeCopy.flagTargetProject, "target-project", "", i18n.G("Move to a project different from the source")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePoolWithVolume(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeMove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeMoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	// Source
	srcServer := parsed[0].RemoteServer
	srcPoolName := parsed[0].RemoteObject.List[0].String
	srcVolName := parsed[0].RemoteObject.List[1].String

	// Destination
	dstServer := parsed[1].RemoteServer
	dstPoolName := parsed[1].RemoteObject.List[0].String
	dstVolName := parsed[1].RemoteObject.List[1].String

	// Rename volume if both remotes and pools of source and target are equal
	// and neither destination cluster member name nor target project are set.
	if srcServer == dstServer && srcPoolName == dstPoolName && c.storageVolume.flagDestinationTarget == "" && c.storageVolumeCopy.flagTargetProject == "" {
		return c.storageVolumeRename.rename(srcServer, srcPoolName, srcVolName, dstVolName)
	}

	return c.storageVolumeCopy.copyOrMove(cmd, parsed)
}

// Rename.
type cmdStorageVolumeRename struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeRenameUsage = u.Usage{u.Pool.Remote(), u.Volume, u.NewName(u.Volume)}

func (c *cmdStorageVolumeRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdStorageVolumeRenameUsage...)
	cmd.Short = i18n.G("Rename custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename custom storage volumes`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

// rename runs the post-parsing command logic.
func (c *cmdStorageVolumeRename) rename(d incus.InstanceServer, poolName string, volName string, newVolName string) error {
	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	err := d.RenameStoragePoolVolume(poolName, "custom", volName, api.StorageVolumePost{Name: newVolName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G(`Renamed storage volume from "%s" to "%s"`)+"\n", volName, newVolName)
	}

	return nil
}

func (c *cmdStorageVolumeRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	newVolName := parsed[2].String

	return c.rename(d, poolName, volName, newVolName)
}

// Set.
type cmdStorageVolumeSet struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagIsProperty bool
}

var cmdStorageVolumeSetUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume, u.Snapshot.Optional()), u.LegacyKV.List(1)}

func (c *cmdStorageVolumeSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdStorageVolumeSetUsage...)
	cmd.Short = i18n.G("Set storage volume configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set storage volume configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus storage volume set [<remote>:]<pool> [<type>/]<volume> <key> <value>

If the type is not specified, Incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume set default data size=1GiB
    Sets the size of a custom volume "data" in pool "default" to 1 GiB

incus storage volume set default virtual-machine/data snapshots.expiry=7d
    Sets the snapshot expiration period for a virtual machine "data" in pool "default" to seven days`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		// TODO all volume config keys

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdStorageVolumeSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String
	isSnapshot := !parsed[1].List[2].Skipped
	snapName := parsed[1].List[2].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	if isSnapshot {
		if c.flagIsProperty {
			snapVol, etag, err := d.GetStoragePoolVolumeSnapshot(poolName, volType, volName, snapName)
			if err != nil {
				return err
			}

			writable := snapVol.Writable()
			if cmd.Name() == "unset" {
				for k := range keys {
					err := unsetFieldByJSONTag(&writable, k)
					if err != nil {
						return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
					}
				}
			} else {
				err := unpackKVToWritable(&writable, keys)
				if err != nil {
					return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
				}
			}

			err = d.UpdateStoragePoolVolumeSnapshot(poolName, volType, volName, snapName, writable, etag)
			if err != nil {
				return err
			}

			return nil
		}

		return errors.New(i18n.G("Snapshots are read-only and can't have their configuration changed"))
	}

	// If a target was specified, create the volume on the given member.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry.
	vol, etag, err := d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		// Give more context on missing volumes.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("Storage pool volume \"%s/%s\" not found", volType, volName)
		}

		return err
	}

	writable := vol.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		// Update the volume config keys.
		maps.Copy(writable.Config, keys)
	}

	err = d.UpdateStoragePoolVolume(poolName, vol.Type, vol.Name, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdStorageVolumeSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdStorageVolumeShow struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

var cmdStorageVolumeShowUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageVolumeShowUsage...)
	cmd.Short = i18n.G("Show storage volume configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show storage volume configurations

If the type is not specified, Incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".

For snapshots, add the snapshot name (only if type is one of custom, container or virtual-machine).`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume show default foo
    Will show the properties of custom volume "foo" in pool "default"

incus storage volume show default virtual-machine/v1
    Will show the properties of the virtual-machine volume "v1" in pool "default"

incus storage volume show default container/c1
    Will show the properties of the container volume "c1" in pool "default"`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

func (c *cmdStorageVolumeShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// If a target member was specified, get the volume with the matching
	// name on that member, if any.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the storage volume entry
	vol, _, err := d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		// Give more context on missing volumes.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			if volType == "custom" {
				return fmt.Errorf("Storage pool volume \"%s/%s\" not found. Try virtual-machine or container for type", volType, volName)
			}

			return fmt.Errorf("Storage pool volume \"%s/%s\" not found", volType, volName)
		}

		return err
	}

	sort.Strings(vol.UsedBy)

	data, err := yaml.Dump(&vol, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdStorageVolumeUnset struct {
	global           *cmdGlobal
	storage          *cmdStorage
	storageVolume    *cmdStorageVolume
	storageVolumeSet *cmdStorageVolumeSet

	flagIsProperty bool
}

var cmdStorageVolumeUnsetUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume, u.Snapshot.Optional()), u.Key}

func (c *cmdStorageVolumeUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdStorageVolumeUnsetUsage...)
	cmd.Short = i18n.G("Unset storage volume configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Unset storage volume configuration keys

If the type is not specified, Incus assumes the type is "custom".
Supported values for type are "custom", "container" and "virtual-machine".`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume unset default foo size
    Removes the size/quota of custom volume "foo" in pool "default"

incus storage volume unset default virtual-machine/v1 snapshots.expiry
    Removes the snapshot expiration period of virtual machine volume "v1" in pool "default"`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage volume property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpStoragePoolVolumeConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.storageVolumeSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.storageVolumeSet, cmd, parsed)
}

// File.
type cmdStorageVolumeFile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagUID  int
	flagGID  int
	flagMode string

	flagMkdir bool
}

func (c *cmdStorageVolumeFile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("file")
	cmd.Short = i18n.G("Manage files in custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage files in custom volumes`))

	// Create
	storageVolumeFileCreateCmd := cmdStorageVolumeFileCreate{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileCreateCmd.command())

	// Delete
	storageVolumeFileDeleteCmd := cmdStorageVolumeFileDelete{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileDeleteCmd.command())

	// Mount
	storageVolumeFileMountCmd := cmdStorageVolumeFileMount{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileMountCmd.command())

	// Pull
	storageVolumeFilePullCmd := cmdStorageVolumeFilePull{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c, puller: &pullable{}}
	cmd.AddCommand(storageVolumeFilePullCmd.command())

	// Push
	storageVolumeFilePushCmd := cmdStorageVolumeFilePush{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFilePushCmd.command())

	// Edit
	storageVolumeFileEditCmd := cmdStorageVolumeFileEdit{global: c.global, filePull: &storageVolumeFilePullCmd, filePush: &storageVolumeFilePushCmd}
	cmd.AddCommand(storageVolumeFileEditCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageVolumeFileCreate struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagForce bool
	flagType  string
}

var cmdStorageVolumeFileCreateUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path), u.SymlinkTargetPath.Optional()}

func (c *cmdStorageVolumeFileCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageVolumeFileCreateUsage...)
	cmd.Short = i18n.G("Create files and directories in custom vollume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create files and directories in custom volume`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume file create foo bar/baz
   To create a file baz in the bar volume on the foo pool.

incus file create --type=symlink foo bar/baz qux
   To create a symlink qux in bar storage volume on the foo pool whose target is baz.`))

	cmd.Flags().BoolVarP(&c.storageVolumeFile.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary")+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force creating files or directories")+"``")
	cmd.Flags().IntVar(&c.storageVolumeFile.flagGID, "gid", -1, i18n.G("Set the file's gid on create")+"``")
	cmd.Flags().IntVar(&c.storageVolumeFile.flagUID, "uid", -1, i18n.G("Set the file's uid on create")+"``")
	cmd.Flags().StringVar(&c.storageVolumeFile.flagMode, "mode", "", i18n.G("Set the file's perms on create")+"``")
	cmd.Flags().StringVar(&c.flagType, "type", "file", i18n.G("The type to create (file, symlink, or directory)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFileCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[0].String
	targetPath, isDir := normalizePath(parsed[1].List[1].String)
	isSymlink := !parsed[2].Skipped
	symlinkTargetPath := filepath.Clean(parsed[2].String)

	if !slices.Contains([]string{"file", "symlink", "directory"}, c.flagType) {
		return fmt.Errorf(i18n.G("Invalid type %q"), c.flagType)
	}

	if isSymlink && c.flagType != "symlink" {
		return errors.New(i18n.G(`Symlink target path can only be used for type "symlink"`))
	}

	if isDir {
		c.flagType = "directory"
	}

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return err
	}

	defer func() { _ = sftpConn.Close() }()

	// Determine the target uid
	uid := max(c.storageVolumeFile.flagUID, 0)

	// Determine the target gid
	gid := max(c.storageVolumeFile.flagGID, 0)

	var mode os.FileMode

	// Determine the target mode
	switch c.flagType {
	case "directory":
		mode = os.FileMode(DirMode)
	case "file":
		mode = os.FileMode(FileMode)
	}

	if c.storageVolumeFile.flagMode != "" {
		if len(c.storageVolumeFile.flagMode) == 3 {
			c.storageVolumeFile.flagMode = "0" + c.storageVolumeFile.flagMode
		}

		m, err := strconv.ParseInt(c.storageVolumeFile.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = os.FileMode(m)
	}

	// Create needed paths if requested
	if c.storageVolumeFile.flagMkdir {
		err := sftpRecursiveMkdir(sftpConn, filepath.Dir(targetPath), nil, int64(uid), int64(gid))
		if err != nil {
			return err
		}
	}

	var content io.ReadSeeker
	var readCloser io.ReadCloser
	var contentLength int64

	switch c.flagType {
	case "symlink":
		content = strings.NewReader(symlinkTargetPath)
		readCloser = io.NopCloser(content)
		contentLength = int64(len(symlinkTargetPath))
	case "file":
		// Just creating an empty file.
		content = strings.NewReader("")
		readCloser = io.NopCloser(content)
		contentLength = 0
	}

	fileArgs := incus.InstanceFileArgs{
		Type:    c.flagType,
		UID:     int64(uid),
		GID:     int64(gid),
		Mode:    int(mode.Perm()),
		Content: content,
	}

	if c.flagForce {
		fileArgs.WriteMode = "overwrite"
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Creating %s: %%s"), targetPath),
		Quiet:  c.global.flagQuiet,
	}

	if readCloser != nil {
		fileArgs.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: readCloser,
			Tracker: &ioprogress.ProgressTracker{
				Length: contentLength,
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, fileArgs.Content)
	}

	err = sftpCreateFile(sftpConn, targetPath, fileArgs, true)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// Delete.
type cmdStorageVolumeFileDelete struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagForce bool
}

var cmdStorageVolumeFileDeleteUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path)}

func (c *cmdStorageVolumeFileDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeFileDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete files in custom volume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete files in custom volume`))

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force deleting files, directories, and subdirectories")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpFiles(toComplete, false)
	}

	return cmd
}

func (c *cmdStorageVolumeFileDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFileDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[0].String
	fPath := parsed[1].List[1].String

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return err
	}

	defer func() { _ = sftpConn.Close() }()

	if c.flagForce {
		err = sftpConn.RemoveAll(fPath)
		if err != nil {
			return err
		}

		return nil
	}

	err = sftpConn.Remove(fPath)
	if err != nil {
		return err
	}

	return nil
}

// Mount.
type cmdStorageVolumeFileMount struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagListen   string
	flagAuthNone bool
	flagAuthUser string
}

var cmdStorageVolumeFileMountUsage = u.Usage{u.Pool.Remote(), u.Volume, u.Target(u.Path).Optional()}

func (c *cmdStorageVolumeFileMount) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("mount", cmdStorageVolumeFileMountUsage...)
	cmd.Short = i18n.G("Mount files from custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Mount files from custom storage volumes.
If no target path is provided, start an SSH SFTP listener instead.`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume file mount mypool myvolume localdir
   To mount the storage volume myvolume from pool mypool onto the local directory localdir.

incus storage volume file mount mypool myvolume
   To start an SSH SFTP listener for the storage volume myvolume from pool mypool.`))

	cmd.Flags().StringVar(&c.flagListen, "listen", "", i18n.G("Setup SSH SFTP listener on address:port instead of mounting"))
	cmd.Flags().BoolVar(&c.flagAuthNone, "no-auth", false, i18n.G("Disable authentication when using SSH SFTP listener"))
	cmd.Flags().StringVar(&c.flagAuthUser, "auth-user", "", i18n.G("Set authentication user when using SSH SFTP listener"))

	cmd.RunE = c.run

	// completion for pool, volume, host path
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileMount) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFileMountUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	hasTargetPath := !parsed[2].Skipped
	targetPath := filepath.Clean(parsed[2].String)
	entity := poolName + "/custom/" + volName

	// Determine the target if specified.
	if hasTargetPath {
		sb, err := os.Stat(targetPath)
		if err != nil {
			return err
		}

		if !sb.IsDir() {
			return errors.New(i18n.G("Target path must be a directory"))
		}

		// Check which mode we should operate in. If target path is provided we use sshfs mode.
		if c.flagListen != "" {
			return errors.New(i18n.G("Target path and --listen flag cannot be used together"))
		}

		// Connect to SFTP.
		sftpConn, err := d.GetStoragePoolVolumeFileSFTPConn(poolName, "custom", volName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
		}

		defer func() { _ = sftpConn.Close() }()

		return sshfsMount(cmd.Context(), sftpConn, entity, "", targetPath)
	}

	// Check if the pool and the volume exist before starting the SFTP server.
	_, _, err = d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return err
	}

	return sshSFTPServer(cmd.Context(), func() (net.Conn, error) {
		return d.GetStoragePoolVolumeFileSFTPConn(poolName, "custom", volName)
	}, c.flagAuthNone, c.flagAuthUser, c.flagListen)
}

// Edit.
type cmdStorageVolumeFileEdit struct {
	global   *cmdGlobal
	filePull *cmdStorageVolumeFilePull
	filePush *cmdStorageVolumeFilePush
}

var cmdStorageVolumeFileEditUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path)}

func (c *cmdStorageVolumeFileEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageVolumeFileEditUsage...)
	cmd.Short = i18n.G("Edit files in storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit files in storage volumes`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFileEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	fPath := parsed[1].List[1].String

	c.filePush.noModeChange = true

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		return c.filePush.push(os.Stdin.Name(), parsed[0], parsed[1])
	}

	// Create temp file
	f, err := os.CreateTemp("", fmt.Sprintf("incus_file_edit_*%s", filepath.Ext(fPath)))
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to create a temporary file: %v"), err)
	}

	fname := f.Name()
	_ = f.Close()
	_ = os.Remove(fname)

	// Tell pull/push that they're called from edit.
	c.filePull.edit = true
	c.filePush.edit = true

	// Extract current value
	defer func() { _ = os.Remove(fname) }()
	err = c.filePull.pull(parsed[0], parsed[1], fname)
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = cli.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	// Push the result
	err = c.filePush.push(fname, parsed[0], parsed[1])
	if err != nil {
		return err
	}

	return nil
}

// Pull.
type cmdStorageVolumeFilePull struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile
	puller            *pullable

	edit bool
}

var cmdStorageVolumeFilePullUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path), u.Target(u.Path)}

func (c *cmdStorageVolumeFilePull) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("pull", cmdStorageVolumeFilePullUsage...)
	cmd.Short = i18n.G("Pull files from custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Pull files from custom volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus custom volume file pull local v1/foo/etc/hosts .
   To pull /etc/hosts from the custom volume and write it to the current directory.

incus file pull local v1 foo/etc/hosts -
   To pull /etc/hosts from the custom volume and write its output to standard output.`))

	cmd.Flags().BoolVarP(&c.storageVolumeFile.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().BoolVarP(&c.puller.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.Flags().BoolVarP(&c.puller.flagNoDereference, "no-dereference", "P", false, i18n.G("Never follow symbolic links in source path")+"``")
	cmd.Flags().BoolVarP(&c.puller.flagFollow, "follow", "H", false, i18n.G("Follow command-line symbolic links in source path")+"``")
	cmd.Flags().BoolVarP(&c.puller.flagDereference, "dereference", "L", false, i18n.G("Always follow symbolic links in source path")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// pull runs the post-parsing command logic.
func (c *cmdStorageVolumeFilePull) pull(parsedPool *u.Parsed, parsedPath *u.Parsed, target string) error {
	d := parsedPool.RemoteServer
	poolName := parsedPool.RemoteObject.String
	volName := parsedPath.List[0].String
	fPath := "/" + parsedPath.List[1].String

	targetIsDir := strings.HasSuffix(target, "/")
	targetExists := true
	targetInfo, err := os.Stat(target)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		targetExists = false
	}

	err = c.puller.preCheck(target)
	if err != nil {
		return err
	}

	/*
	 * If the path exists, just use it. If it doesn't exist, it might be a
	 * directory in one of two cases:
	 *   1. Someone explicitly put "/" at the end
	 *   2. We are dealing with recursive copy
	 */
	if targetExists {
		targetIsDir = targetInfo.IsDir()
	} else if targetIsDir {
		err := os.MkdirAll(target, DirMode)
		if err != nil {
			return err
		}
	} else if c.storageVolumeFile.flagMkdir {
		err := os.MkdirAll(filepath.Dir(target), DirMode)
		if err != nil {
			return err
		}
	}

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
	}

	defer func() { _ = sftpConn.Close() }()

	srcInfo, fPath, err := c.puller.statFile(sftpConn, fPath)
	if err != nil {
		return err
	}

	// Recursively copy directories.
	if srcInfo.IsDir() {
		return sftpRecursivePullFile(sftpConn, srcInfo, fPath, target, c.global.flagQuiet, c.puller.flagDereference, util.PathExists(target))
	}

	var targetPath string
	if targetIsDir {
		targetPath = filepath.Join(target, filepath.Base(fPath))
	} else {
		targetPath = target
	}

	// Prepare target.
	targetIsLink := srcInfo.Mode()&os.ModeSymlink != 0
	var f *os.File
	var linkName string

	if isStdout(targetPath) {
		f = os.Stdout
	} else if targetIsLink {
		linkName, err = sftpConn.ReadLink(fPath)
		if err != nil {
			return err
		}
	} else {
		f, err = os.Create(targetPath)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }() // nolint:revive

		err = os.Chmod(targetPath, os.FileMode(srcInfo.Mode()))
		if err != nil {
			return err
		}
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), targetPath, fPath),
		Quiet:  c.global.flagQuiet,
	}

	writer := &ioprogress.ProgressWriter{
		WriteCloser: f,
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(bytesReceived int64, speed int64) {
				if isStdout(targetPath) {
					return
				}

				progress.UpdateProgress(ioprogress.ProgressData{
					Text: fmt.Sprintf("%s (%s/s)",
						units.GetByteSizeString(bytesReceived, 2),
						units.GetByteSizeString(speed, 2)),
				})
			},
		},
	}

	if targetIsLink {
		err = os.Symlink(linkName, targetPath)
		if err != nil {
			progress.Done("")
			return err
		}
	} else {
		src, err := sftpConn.Open(fPath)
		if err != nil {
			return err
		}

		defer func() { _ = src.Close() }()

		_, err = util.SafeCopy(writer, src)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	progress.Done("")
	return nil
}

func (c *cmdStorageVolumeFilePull) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFilePullUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.pull(parsed[0], parsed[1], parsed[2].String)
}

// Push.
type cmdStorageVolumeFilePush struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	edit         bool
	noModeChange bool

	flagRecursive bool
}

var cmdStorageVolumeFilePushUsage = u.Usage{u.Path, u.Pool.Remote(), u.MakePath(u.Volume, u.Target(u.Path))}

func (c *cmdStorageVolumeFilePush) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("push", cmdStorageVolumeFilePushUsage...)
	cmd.Short = i18n.G("Push files into custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Push files into custom volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume file push /etc/hosts local v1/etc/hosts
   To push /etc/hosts into the custom volume "v1".

echo "Hello world" | incus storage volume file push - local v1 test
   To read "Hello world" from standard input and write it into test in volume "v1".`))

	cmd.Flags().BoolVarP(&c.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.Flags().BoolVarP(&c.storageVolumeFile.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().IntVar(&c.storageVolumeFile.flagUID, "uid", -1, i18n.G("Set the file's uid on push")+"``")
	cmd.Flags().IntVar(&c.storageVolumeFile.flagGID, "gid", -1, i18n.G("Set the file's gid on push")+"``")
	cmd.Flags().StringVar(&c.storageVolumeFile.flagMode, "mode", "", i18n.G("Set the file's perms on push")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// push runs the post-parsing command logic.
func (c *cmdStorageVolumeFilePush) push(srcFile string, parsedPool *u.Parsed, parsedTarget *u.Parsed) error {
	d := parsedPool.RemoteServer
	poolName := parsedPool.RemoteObject.String
	volName := parsedTarget.List[0].String
	targetPath, targetIsDir := normalizePath(parsedTarget.List[1].String)

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
	}

	defer func() { _ = sftpConn.Close() }()

	// Determine the target mode
	mode := os.FileMode(DirMode)
	if c.storageVolumeFile.flagMode != "" {
		if len(c.storageVolumeFile.flagMode) == 3 {
			c.storageVolumeFile.flagMode = "0" + c.storageVolumeFile.flagMode
		}

		m, err := strconv.ParseInt(c.storageVolumeFile.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = os.FileMode(m)
	}

	// Recursive calls
	if c.flagRecursive {
		// Quick checks.
		if c.storageVolumeFile.flagUID != -1 || c.storageVolumeFile.flagGID != -1 || c.storageVolumeFile.flagMode != "" {
			return errors.New(i18n.G("Can't supply uid/gid/mode in recursive mode"))
		}

		// Create needed paths if requested
		if c.storageVolumeFile.flagMkdir {
			f, err := os.Open(srcFile)
			if err != nil {
				return err
			}

			finfo, err := f.Stat()
			_ = f.Close()
			if err != nil {
				return err
			}

			mode, uid, gid := internalIO.GetOwnerMode(finfo)

			err = sftpRecursiveMkdir(sftpConn, targetPath, &mode, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		// Transfer the file
		err := sftpRecursivePushFile(sftpConn, srcFile, srcFile, targetPath, c.global.flagQuiet, true, true)
		if err != nil {
			return err
		}

		return nil
	}

	// Determine the target uid
	uid := max(c.storageVolumeFile.flagUID, 0)

	// Determine the target gid
	gid := max(c.storageVolumeFile.flagGID, 0)

	// Make sure the file is accessible by us before trying to push it
	var f *os.File
	if srcFile == "-" {
		f = os.Stdin
	} else {
		f, err = os.Open(srcFile)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %s %w"), srcFile, err)
		}
	}

	defer func() { _ = f.Close() }() // nolint:revive

	// Push the file
	fpath := targetPath
	if targetIsDir {
		fpath = filepath.Join(fpath, filepath.Base(f.Name()))
	}

	// Create needed paths if requested
	if c.storageVolumeFile.flagMkdir {
		finfo, err := f.Stat()
		if err != nil {
			return err
		}

		if c.storageVolumeFile.flagUID == -1 || c.storageVolumeFile.flagGID == -1 {
			_, dUID, dGID := internalIO.GetOwnerMode(finfo)

			if c.storageVolumeFile.flagUID == -1 {
				uid = dUID
			}

			if c.storageVolumeFile.flagGID == -1 {
				gid = dGID
			}
		}

		err = sftpRecursiveMkdir(sftpConn, filepath.Dir(fpath), nil, int64(uid), int64(gid))
		if err != nil {
			return err
		}
	}

	// Transfer the file
	fileArgs := incus.InstanceFileArgs{
		UID:  -1,
		GID:  -1,
		Mode: -1,
	}

	if !c.noModeChange {
		if c.storageVolumeFile.flagMode == "" || c.storageVolumeFile.flagUID == -1 || c.storageVolumeFile.flagGID == -1 {
			finfo, err := f.Stat()
			if err != nil {
				return err
			}

			fMode, fUID, fGID := internalIO.GetOwnerMode(finfo)

			if c.storageVolumeFile.flagMode == "" {
				mode = fMode
			}

			if c.storageVolumeFile.flagUID == -1 {
				uid = fUID
			}

			if c.storageVolumeFile.flagGID == -1 {
				gid = fGID
			}
		}

		fileArgs.UID = int64(uid)
		fileArgs.GID = int64(gid)
		fileArgs.Mode = int(mode.Perm())
	}

	fileArgs.Type = "file"

	fstat, err := f.Stat()
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), f.Name(), fpath),
		Quiet:  c.global.flagQuiet,
	}

	fileArgs.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
		ReadCloser: f,
		Tracker: &ioprogress.ProgressTracker{
			Length: fstat.Size(),
			Handler: func(percent int64, speed int64) {
				progress.UpdateProgress(ioprogress.ProgressData{
					Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
				})
			},
		},
	}, f)

	logger.Infof("Pushing %s to %s (%s)", f.Name(), fpath, fileArgs.Type)
	err = sftpCreateFile(sftpConn, fpath, fileArgs, true)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

func (c *cmdStorageVolumeFilePush) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeFilePushUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.push(parsed[0].String, parsed[1], parsed[2])
}

// Snapshot.
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
	storageVolumeSnapshotListCmd := cmdStorageVolumeSnapshotList{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeSnapshot: c}
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

var cmdStorageVolumeSnapshotCreateUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.NewName(u.Snapshot).Optional()}

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

	cmd.Flags().StringVar(&c.flagExpiry, "expiry", "", i18n.G("Expiry date or time span for the new snapshot"))
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the storage volume"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the snapshot name already exists, delete and create a new one"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Snapshot description")+"``")

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
	parsed, err := cmdStorageVolumeSnapshotCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
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

var cmdStorageVolumeSnapshotDeleteUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Snapshot}

func (c *cmdStorageVolumeSnapshotDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeSnapshotDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete storage volume snapshots`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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
	parsed, err := cmdStorageVolumeSnapshotDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
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

	flagFormat      string
	flagColumns     string
	flagAllProjects bool

	defaultColumns string
}

var cmdStorageVolumeSnapshotListUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

func (c *cmdStorageVolumeSnapshotList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageVolumeSnapshotListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage volume snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List storage volume snapshots`))

	c.defaultColumns = "nTE"
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", c.defaultColumns, i18n.G("Columns")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("All projects")+"``")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List storage volume snapshots

	The -c option takes a (optionally comma-separated) list of arguments
	that control which image attributes to output when displaying in table
	or csv format.

	Column shorthand chars:
		n - Name
		T - Taken at
		E - Expiry`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

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

func (c *cmdStorageVolumeSnapshotList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeSnapshotListUsage.Parse(c.global.conf, cmd, args)
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

	return c.listSnapshots(d, poolName, volType, volName)
}

func (c *cmdStorageVolumeSnapshotList) listSnapshots(d incus.InstanceServer, poolName string, volumeType string, volumeName string) error {
	snapshots, err := d.GetStoragePoolVolumeSnapshots(poolName, volumeType, volumeName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, snap := range snapshots {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(snap))
		}

		data = append(data, line)
	}

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, snapshots)
}

type storageVolumeSnapshotColumn struct {
	Name string
	Data func(api.StorageVolumeSnapshot) string
}

func (c *cmdStorageVolumeSnapshotList) parseColumns() ([]storageVolumeSnapshotColumn, error) {
	columnsShorthandMap := map[rune]storageVolumeSnapshotColumn{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'T': {i18n.G("TAKEN AT"), c.takenAtColumnData},
		'E': {i18n.G("EXPIRES AT"), c.expiresAtColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []storageVolumeSnapshotColumn{}

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

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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
	parsed, err := cmdStorageVolumeSnapshotRenameUsage.Parse(c.global.conf, cmd, args)
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
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

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
	parsed, err := cmdStorageVolumeSnapshotRestoreUsage.Parse(c.global.conf, cmd, args)
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
		`Show storage volume snapshhot configurations`))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

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
	parsed, err := cmdStorageVolumeSnapshotShowUsage.Parse(c.global.conf, cmd, args)
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

	data, err := yaml.Dump(&vol, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Export.
type cmdStorageVolumeExport struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagVolumeOnly           bool
	flagOptimizedStorage     bool
	flagCompressionAlgorithm string
	flagForce                bool
}

var cmdStorageVolumeExportUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Verbatim("custom").Optional(), u.Volume), u.Target(u.File).Optional()}

func (c *cmdStorageVolumeExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("export", cmdStorageVolumeExportUsage...)
	cmd.Short = i18n.G("Export custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Export custom storage volumes.`))

	cmd.Flags().BoolVar(&c.flagVolumeOnly, "volume-only", false, i18n.G("Export the volume without its snapshots (ignored for ISO storage volumes)"))
	cmd.Flags().BoolVar(&c.flagOptimizedStorage, "optimized-storage", false,
		i18n.G("Use storage driver optimized format (can only be restored on a similar pool, ignored for ISO storage volumes)"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (none for uncompressed, ignored for ISO storage volumes)")+"``")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagForce, "force", false, i18n.G("Force overwriting existing backup file"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdStorageVolumeExport) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeExportUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[1].String
	hasTarget := !parsed[2].Skipped
	targetName := parsed[2].Get("." + volName + ".backup")

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	volumeOnly := c.flagVolumeOnly

	// Get the storage volume entry.
	vol, _, err := d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return fmt.Errorf("Storage pool volume \"custom/%s\" not found", volName)
	}

	if !hasTarget && vol.ContentType == "iso" {
		targetName = volName + ".iso"
		hasTarget = true
	}

	if isStdout(targetName) {
		// If outputting to stdout, quiesce the output.
		c.global.flagQuiet = true
	} else if hasTarget && !c.flagForce && util.PathExists(targetName) {
		// Check if the target path already exists.
		return fmt.Errorf(i18n.G("Target path %q already exists"), targetName)
	}

	req := api.StorageVolumeBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(24 * time.Hour),
		VolumeOnly:           volumeOnly,
		OptimizedStorage:     c.flagOptimizedStorage,
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	var getter func(backupReq *incus.BackupFileRequest) error

	if d.HasExtension("direct_backup") {
		getter = func(backupReq *incus.BackupFileRequest) error {
			return d.CreateStorageVolumeBackupStream(poolName, volName, req, backupReq)
		}
	} else {
		op, err := d.CreateStorageVolumeBackup(poolName, volName, req)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create storage volume backup: %w"), err)
		}

		// Watch the background operation.
		progress := cli.ProgressRenderer{
			Format: i18n.G("Backing up storage volume: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait until backup is done.
		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")

		err = op.Wait()
		if err != nil {
			return err
		}

		// Get name of backup.
		uStr := op.Get().Resources["backups"][0]
		uri, err := url.Parse(uStr)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid URL %q: %w"), uStr, err)
		}

		backupName, err := url.PathUnescape(path.Base(uri.EscapedPath()))
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid backup name segment in path %q: %w"), uri.EscapedPath(), err)
		}

		defer func() {
			// Delete backup after we're done.
			op, err = d.DeleteStorageVolumeBackup(poolName, volName, backupName)
			if err == nil {
				_ = op.Wait()
			}
		}()

		getter = func(backupReq *incus.BackupFileRequest) error {
			_, err := d.GetStorageVolumeBackupFile(poolName, volName, backupName, backupReq)
			return err
		}
	}

	var target *os.File
	if isStdout(targetName) {
		target = os.Stdout
	} else {
		target, err = os.Create(targetName)
		if err != nil {
			return err
		}

		defer func() { _ = target.Close() }()
	}

	// Prepare the download request.
	progress := cli.ProgressRenderer{
		Format: i18n.G("Exporting the backup: %s"),
		Quiet:  c.global.flagQuiet,
	}

	backupFileRequest := incus.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball
	err = getter(&backupFileRequest)
	if err != nil {
		_ = os.Remove(targetName)
		progress.Done("")
		return fmt.Errorf(i18n.G("Failed to fetch storage volume backup file: %w"), err)
	}

	// Detect backup file type and rename file accordingly.
	if !hasTarget {
		_, err := target.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		_, ext, _, err := archive.DetectCompressionFile(target)
		if err != nil {
			return err
		}

		err = os.Rename(targetName, volName+ext)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to rename export file: %w"), err)
		}
	}

	err = target.Close()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to close export file: %w"), err)
	}

	progress.Done(i18n.G("Backup exported successfully!"))
	return nil
}

// Import.
type cmdStorageVolumeImport struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagType string
}

var cmdStorageVolumeImportUsage = u.Usage{u.Pool.Remote(), u.BackupFile, u.NewName(u.Volume).Optional()}

func (c *cmdStorageVolumeImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("import", cmdStorageVolumeImportUsage...)
	cmd.Short = i18n.G("Import custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Import custom storage volumes.`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume import default backup0.tar.gz
    Create a new custom volume using backup0.tar.gz as the source

incus storage volume import default some-installer.iso installer --type=iso
    Create a new custom volume storing some-installer.iso for use as a CD-ROM image`))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagType, "type", "", i18n.G("Import type, backup or iso (default \"backup\")")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdStorageVolumeImport) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeImportUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	backupFile := parsed[1].String
	hasVolName := !parsed[2].Skipped
	volName := parsed[2].String

	// Use the provided target.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	var file *os.File
	if isStdin(backupFile) {
		file = os.Stdin
	} else {
		file, err = os.Open(backupFile)
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()
	}

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	if c.flagType == "" {
		// Set type to iso if filename suffix is .iso.
		if strings.HasSuffix(strings.ToLower(backupFile), ".iso") {
			c.flagType = "iso"
		} else {
			c.flagType = "backup"
		}
	} else {
		// Validate type flag.
		if !slices.Contains([]string{"backup", "iso"}, c.flagType) {
			return errors.New(i18n.G("Import type needs to be \"backup\" or \"iso\""))
		}
	}

	if c.flagType == "iso" && !hasVolName {
		volName = strings.TrimSuffix(filepath.Base(backupFile), filepath.Ext(backupFile))
	}

	progress := cli.ProgressRenderer{
		Format: i18n.G("Importing custom volume: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := incus.StorageVolumeBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		},
		Name: volName,
	}

	var op incus.Operation

	if c.flagType == "iso" {
		op, err = d.CreateStoragePoolVolumeFromISO(poolName, createArgs)
	} else {
		op, err = d.CreateStoragePoolVolumeFromBackup(poolName, createArgs)
	}

	if err != nil {
		return err
	}

	// Wait for operation to finish.
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// NBD.
type cmdStorageVolumeNBD struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagAddress  string
	flagWritable bool
}

var cmdStorageVolumeNBDUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.StorageVolumeType.Optional(), u.Volume)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageVolumeNBD) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("nbd", cmdStorageVolumeNBDUsage...)
	cmd.Short = i18n.G("NBD access to a block storage volume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`NBD access to a block storage volume.`))

	cmd.Flags().StringVar(&c.flagAddress, "address", "", i18n.G("Specific address to listen on"))
	cmd.Flags().BoolVar(&c.flagWritable, "writable", false, i18n.G("Get write access to the disk"))

	cmd.RunE = c.Run

	// completion for pool, volume, host path
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

// Run runs the actual command logic.
func (c *cmdStorageVolumeNBD) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageVolumeNBDUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volType := parsed[1].List[0].Get("custom")
	volName := parsed[1].List[1].String

	// Check if the pool and the volume exist before starting the SFTP server.
	_, _, err = d.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		return err
	}

	// Proxy to a local listener.
	listenAddr := c.flagAddress

	if listenAddr == "" {
		listenAddr = "127.0.0.1:0" // Listen on a random local port if not specified.
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf(i18n.G("NBD listening on %v")+"\n", listener.Addr())

	// Wait for a connection.
	nConn, err := listener.Accept()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
	}

	defer func() { _ = nConn.Close() }()

	fmt.Printf(i18n.G("NBD client connected %q")+"\n", nConn.RemoteAddr())
	defer fmt.Printf(i18n.G("NBD client disconnected %q")+"\n", nConn.RemoteAddr())

	// Connect to NBD.
	conn, err := d.GetStoragePoolVolumeBlockNBDConn(poolName, volType, volName, incus.StorageVolumeNBDPost{Writable: c.flagWritable})
	if err != nil {
		return fmt.Errorf(i18n.G("NBD connection failed: %v")+"\n", err)
	}

	defer func() { _ = conn.Close() }()

	// Proxy the traffic.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()

		_, _ = util.SafeCopy(conn, nConn)
		_ = conn.Close()
		_ = nConn.Close()
	}()

	go func() {
		defer wg.Done()

		_, _ = util.SafeCopy(nConn, conn)
		_ = conn.Close()
		_ = nConn.Close()
	}()

	wg.Wait()

	return nil
}
