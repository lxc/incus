package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdCopy struct {
	global *cmdGlobal

	flagNoProfiles          bool
	flagProfile             []string
	flagConfig              []string
	flagDevice              []string
	flagEphemeral           bool
	flagInstanceOnly        bool
	flagMode                string
	flagStateless           bool
	flagStorage             string
	flagTarget              string
	flagTargetProject       string
	flagRefresh             bool
	flagRefreshExcludeOlder bool
	flagAllowInconsistent   bool
}

var cmdCopyUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Remote(), u.NewName(u.Instance).Optional().Remote()}

func (c *cmdCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("copy", cmdCopyUsage...)
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy instances within or in between servers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Copy instances within or in between servers

Transfer modes (--mode):
 - pull: Target server pulls the data from the source server (source must listen on network)
 - push: Source server pushes the data to the target server (target must listen on network)
 - relay: The CLI connects to both source and server and proxies the data (both source and target must listen on network)

The pull transfer mode is the default as it is compatible with all server versions.
`))

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new instance")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral instance"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull, push or relay")+"``")
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, i18n.G("Copy the instance without its snapshots"))
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful instance stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the instance with no profiles applied"))
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Perform an incremental copy"))
	cmd.Flags().BoolVar(&c.flagRefreshExcludeOlder, "refresh-exclude-older", false, i18n.G("During incremental copy, exclude source snapshots earlier than latest target snapshot"))
	cmd.Flags().BoolVar(&c.flagAllowInconsistent, "allow-inconsistent", false, i18n.G("Ignore copy errors for volatile files"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// copyOrMove runs the post-parsing command logic.
func (c *cmdCopy) copyOrMove(cmd *cobra.Command, src *u.Parsed, dst *u.Parsed, keepVolatile bool, ephemeral int, stateful bool, instanceOnly bool, mode string, pool string, move bool) error {
	srcServer := src.RemoteServer
	srcInstanceName := src.RemoteObject.String

	// This function can be called from both the `copy` and `move` commands. As their first arguments
	// have a different grammar, additional care is taken here to normalize them.
	srcIsSnapshot := false
	srcSnapName := ""
	if cmd.Name() == "copy" {
		srcInstanceName = src.RemoteObject.List[0].String
		srcIsSnapshot = !src.RemoteObject.List[1].Skipped
		srcSnapName = src.RemoteObject.List[1].String
	}

	dstServer := dst.RemoteServer
	hasDstInstance := !dst.RemoteObject.Skipped
	dstInstanceName := dst.RemoteObject.String

	// Don't allow refreshing without profiles.
	if c.flagRefresh && c.flagNoProfiles {
		return errors.New(i18n.G("--no-profiles cannot be used with --refresh"))
	}

	// If the instance is being copied to a different remote and no destination name is
	// specified, use the source name.
	if !hasDstInstance {
		if srcServer == dstServer && c.flagTarget == "" {
			return errors.New(i18n.G("You must specify a destination instance name"))
		}

		dstInstanceName = srcInstanceName
	}

	// Project copies
	if c.flagTargetProject != "" {
		dstServer = dstServer.UseProject(c.flagTargetProject)
	}

	// Confirm that --target is only used with a cluster
	if c.flagTarget != "" && !dstServer.IsClustered() {
		return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
	}

	// Parse the config overrides
	configMap := map[string]string{}
	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
		}

		configMap[key] = value
	}

	deviceMap, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return err
	}

	var op incus.RemoteOperation
	var writable api.InstancePut
	var start bool

	if srcIsSnapshot {
		if instanceOnly {
			return errors.New(i18n.G("--instance-only can't be passed when the source is a snapshot"))
		}

		// Prepare the instance creation request
		args := incus.InstanceSnapshotCopyArgs{
			Name: dstInstanceName,
			Mode: mode,
			Live: stateful,
		}

		if c.flagRefresh {
			return errors.New(i18n.G("--refresh can only be used with instances"))
		}

		// Copy of a snapshot into a new instance
		entry, _, err := srcServer.GetInstanceSnapshot(srcInstanceName, srcSnapName)
		if err != nil {
			return err
		}

		// Overwrite profiles.
		if c.flagProfile != nil {
			entry.Profiles = c.flagProfile
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		maps.Copy(entry.Config, configMap)

		// Allow setting device overrides
		for k, m := range deviceMap {
			if entry.Devices[k] == nil {
				entry.Devices[k] = m
				continue
			}

			if m["type"] == "none" {
				// When overriding with "none" type, clear the entire device.
				entry.Devices[k] = map[string]string{"type": "none"}
				continue
			}

			maps.Copy(entry.Devices[k], m)
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := instance.GetRootDiskDevice(entry.Devices)

		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		if entry.Config != nil {
			// Strip the last_state.power key in all cases
			delete(entry.Config, "volatile.last_state.power")

			if !keepVolatile {
				for k := range entry.Config {
					if !instance.InstanceIncludeWhenCopying(k, true) {
						delete(entry.Config, k)
					}
				}
			}
		}

		// Do the actual copy
		if c.flagTarget != "" {
			dstServer = dstServer.UseTarget(c.flagTarget)
		}

		op, err = dstServer.CopyInstanceSnapshot(srcServer, srcInstanceName, *entry, &args)
		if err != nil {
			return err
		}
	} else {
		// Prepare the instance creation request
		args := incus.InstanceCopyArgs{
			Name:                dstInstanceName,
			Live:                stateful,
			InstanceOnly:        instanceOnly,
			Mode:                mode,
			Refresh:             c.flagRefresh,
			RefreshExcludeOlder: c.flagRefreshExcludeOlder,
			AllowInconsistent:   c.flagAllowInconsistent,
		}

		// Copy of an instance into a new instance
		entry, _, err := srcServer.GetInstance(srcInstanceName)
		if err != nil {
			return err
		}

		// Only start the instance back up if doing a stateless migration.
		// It's the server's job to start things back up when receiving a stateful migration.
		if entry.StatusCode == api.Running && move && !stateful {
			start = true
		}

		// Overwrite profiles.
		if c.flagProfile != nil {
			entry.Profiles = c.flagProfile
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		maps.Copy(entry.Config, configMap)

		// Allow setting device overrides
		for k, m := range deviceMap {
			if entry.Devices[k] == nil {
				entry.Devices[k] = m
				continue
			}

			if m["type"] == "none" {
				// When overriding with "none" type, clear the entire device.
				entry.Devices[k] = map[string]string{"type": "none"}
				continue
			}

			maps.Copy(entry.Devices[k], m)
		}

		// Allow overriding the ephemeral status
		switch ephemeral {
		case 1:
			entry.Ephemeral = true
		case 0:
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := instance.GetRootDiskDevice(entry.Devices)
		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if !instance.InstanceIncludeWhenCopying(k, true) {
					delete(entry.Config, k)
				}
			}
		}

		if entry.Config != nil {
			// Strip the last_state.power key in all cases
			delete(entry.Config, "volatile.last_state.power")
		}

		// Do the actual copy
		if c.flagTarget != "" {
			dstServer = dstServer.UseTarget(c.flagTarget)
		}

		op, err = dstServer.CopyInstance(srcServer, *entry, &args)
		if err != nil {
			return err
		}

		writable = entry.Writable()
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Transferring instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	if c.flagRefresh {
		inst, etag, err := dstServer.GetInstance(dstInstanceName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to refresh target instance '%s': %v"), dstInstanceName, err)
		}

		// Ensure we don't change the target's volatile.idmap.next value.
		if inst.Config["volatile.idmap.next"] != writable.Config["volatile.idmap.next"] {
			writable.Config["volatile.idmap.next"] = inst.Config["volatile.idmap.next"]
		}

		// Ensure we don't change the target's root disk pool.
		srcRootDiskDeviceKey, _, _ := instance.GetRootDiskDevice(writable.Devices)
		destRootDiskDeviceKey, destRootDiskDevice, _ := instance.GetRootDiskDevice(inst.Devices)
		if srcRootDiskDeviceKey != "" && srcRootDiskDeviceKey == destRootDiskDeviceKey {
			writable.Devices[destRootDiskDeviceKey]["pool"] = destRootDiskDevice["pool"]
		}

		op, err := dstServer.UpdateInstance(dstInstanceName, writable, etag)
		if err != nil {
			return err
		}

		// Watch the background operation
		progress := cli.ProgressRenderer{
			Format: i18n.G("Refreshing instance: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait for the copy to complete
		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	}

	// Start the instance if needed
	if start {
		req := api.InstanceStatePut{
			Action: string(instance.Start),
		}

		op, err := dstServer.UpdateInstanceState(dstInstanceName, req, "")
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdCopy) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdCopyUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	// For copies, default to non-ephemeral and allow override (move uses -1)
	ephem := 0
	if c.flagEphemeral {
		ephem = 1
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	stateful := !c.flagStateless && !c.flagRefresh
	keepVolatile := c.flagRefresh
	instanceOnly := c.flagInstanceOnly

	return c.copyOrMove(cmd, parsed[0], parsed[1], keepVolatile, ephem, stateful, instanceOnly, mode, c.flagStorage, false)
}
