package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdMove struct {
	global *cmdGlobal

	flagNoProfiles        bool
	flagProfile           []string
	flagConfig            []string
	flagInstanceOnly      bool
	flagDevice            []string
	flagMode              string
	flagStateless         bool
	flagStorage           string
	flagTarget            string
	flagTargetProject     string
	flagAllowInconsistent bool
}

var cmdMoveUsage = u.Usage{u.Instance.Remote(), u.NewName(u.Instance).Optional().Remote()}

func (c *cmdMove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("move", cmdMoveUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Move instances within or in between servers")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Move instances within or in between servers

Transfer modes (--mode):
 - pull: Target server pulls the data from the source server (source must listen on network)
 - push: Source server pushes the data to the target server (target must listen on network)
 - relay: The CLI connects to both source and server and proxies the data (both source and target must listen on network)

The pull transfer mode is the default as it is compatible with all server versions.
`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus move [<remote>:]<source instance> [<remote>:][<destination instance>] [--instance-only]
    Move an instance between two hosts, renaming it if destination name differs.

incus move <old name> <new name> [--instance-only]
    Rename a local instance.`))

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the target instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the target instance")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Unset all profiles on the target instance"))
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false, i18n.G("Move the instance without its snapshots"))
	cmd.Flags().StringVar(&c.flagMode, "mode", moveDefaultMode, i18n.G("Transfer mode. One of pull, push or relay.")+"``")
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful instance stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
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

func (c *cmdMove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdMoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return nil
	}

	srcServer := parsed[0].RemoteServer
	srcInstanceName := parsed[0].RemoteObject.String
	dstServer := parsed[1].RemoteServer
	hasDstInstance := !parsed[1].RemoteObject.Skipped
	dstInstanceName := parsed[1].RemoteObject.String

	// Parse the mode
	mode := moveDefaultMode
	if c.flagMode != "" {
		mode = c.flagMode
	}

	// As an optimization, if the source and destination are the same, do
	// this via a simple rename. This only works for instances that aren't
	// running, instances that are running should be live migrated (of
	// course, this changing of hostname isn't supported right now, so this
	// simply won't work).
	if srcServer == dstServer && c.flagTarget == "" && c.flagStorage == "" && c.flagTargetProject == "" {
		if !hasDstInstance {
			return errors.New(i18n.G("Can't perform local rename without a new instance name"))
		}

		if c.flagConfig != nil || c.flagDevice != nil || c.flagProfile != nil || c.flagNoProfiles {
			return errors.New(i18n.G("Can't override configuration or profiles in local rename"))
		}

		// Instance rename
		op, err := srcServer.RenameInstance(srcInstanceName, api.InstancePost{Name: dstInstanceName})
		if err != nil {
			return err
		}

		return op.Wait()
	}

	stateful := !c.flagStateless

	isServerSide := func() bool {
		// Check if same source and destination.
		if srcServer != dstServer {
			return false
		}

		// Check if asked for specific client mode.
		if c.flagMode != moveDefaultMode {
			return false
		}

		// Check if override is requested with a server lacking support.
		if !srcServer.HasExtension("instance_move_config") {
			if len(c.flagConfig) > 0 {
				return false
			}

			if len(c.flagDevice) > 0 {
				return false
			}

			if len(c.flagProfile) > 0 {
				return false
			}
		}

		// Check if server supports moving pools.
		if c.flagStorage != "" && !srcServer.HasExtension("instance_pool_move") {
			return false
		}

		// Check if server supports moving projects.
		if c.flagTargetProject != "" && !srcServer.HasExtension("instance_project_move") {
			return false
		}

		return true
	}()

	// Support for server-side move in clusters.
	if isServerSide {
		return c.moveInstance(parsed[0], parsed[1], stateful)
	}

	cpy := cmdCopy{}
	cpy.global = c.global
	cpy.flagTarget = c.flagTarget
	cpy.flagTargetProject = c.flagTargetProject
	cpy.flagConfig = c.flagConfig
	cpy.flagDevice = c.flagDevice
	cpy.flagProfile = c.flagProfile
	cpy.flagNoProfiles = c.flagNoProfiles
	cpy.flagAllowInconsistent = c.flagAllowInconsistent

	instanceOnly := c.flagInstanceOnly

	// A move is just a copy followed by a delete; however, we want to
	// keep the volatile entries around since we are moving the instance.
	err = cpy.copyOrMove(cmd, parsed[0], parsed[1], true, -1, stateful, instanceOnly, mode, c.flagStorage, true)
	if err != nil {
		return err
	}

	del := cmdDelete{global: c.global}
	del.flagForce = true
	del.flagForceProtected = true
	err = del.deleteOne(parsed[0])
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to delete original instance after copying it: %w"), err)
	}

	return nil
}

// Move an instance between pools and projects using special POST /instances/<name> API.
func (c *cmdMove) moveInstance(src *u.Parsed, dst *u.Parsed, stateful bool) error {
	srcServer := src.RemoteServer
	srcInstanceName := src.RemoteObject.String
	dstInstanceName := dst.RemoteObject.String
	if dst.RemoteObject.Skipped {
		dstInstanceName = srcInstanceName
	}

	if !srcServer.IsClustered() && c.flagTarget != "" {
		return errors.New(i18n.G("--target can only be used with clusters"))
	}

	// Set the target if specified.
	if c.flagTarget != "" {
		srcServer = srcServer.UseTarget(c.flagTarget)
	}

	// Pass the new pool to the migration API.
	req := api.InstancePost{
		Name:         dstInstanceName,
		Migration:    true,
		InstanceOnly: c.flagInstanceOnly,
		Pool:         c.flagStorage,
		Project:      c.flagTargetProject,
		Live:         stateful,
	}

	// Override profiles.
	var profiles *[]string
	if len(c.flagProfile) > 0 {
		profiles = &c.flagProfile
	} else if c.flagNoProfiles {
		profiles = &[]string{}
	}

	if profiles != nil {
		req.Profiles = *profiles
	}

	// Override config.
	if len(c.flagConfig) > 0 {
		req.Config = map[string]string{}

		for _, entry := range c.flagConfig {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
			}

			req.Config[key] = value
		}
	}

	// Override devices.
	if len(c.flagDevice) > 0 {
		req.Devices = map[string]map[string]string{}

		// Parse the overrides.
		deviceMap, err := parseDeviceOverrides(c.flagDevice)
		if err != nil {
			return err
		}

		// Fetch the current instance.
		inst, _, err := srcServer.GetInstance(srcInstanceName)
		if err != nil {
			return err
		}

		for devName, dev := range deviceMap {
			fullDev := inst.ExpandedDevices[devName]
			maps.Copy(fullDev, dev)

			req.Devices[devName] = fullDev
		}
	}

	// Move the instance.
	op, err := srcServer.MigrateInstance(srcInstanceName, req)
	if err != nil {
		return fmt.Errorf(i18n.G("Migration API failure: %w"), err)
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

	// Wait for the move to complete
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return fmt.Errorf(i18n.G("Migration operation failure: %w"), err)
	}

	progress.Done("")

	return nil
}

// Default migration mode when moving an instance.
const moveDefaultMode = "pull"
