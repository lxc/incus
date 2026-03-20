package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

// Rebuild.
type cmdRebuild struct {
	global    *cmdGlobal
	flagEmpty bool
	flagForce bool
}

var cmdRebuildUsage = u.Usage{u.Either(u.Flag("empty"), u.RemoteImage), u.Instance.Remote()}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRebuild) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rebuild", cmdRebuildUsage...)
	cmd.Short = i18n.G("Rebuild instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Wipe the instance root disk and re-initialize with a new image (or empty volume).`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagEmpty, "empty", false, i18n.G("Rebuild as an empty instance"))
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("If an instance is running, stop it and then rebuild it"))

	return cmd
}

// Run runs the actual command logic.
func (c *cmdRebuild) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	parsed, err := cmdRebuildUsage.Parse(conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[1].RemoteServer
	remoteName := parsed[1].RemoteName
	instanceName := parsed[1].RemoteObject.String

	current, _, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	// If the instance is running, stop it first.
	if c.flagForce && current.StatusCode == api.Running {
		req := api.InstanceStatePut{
			Action: "stop",
			Force:  true,
		}

		// Update the instance.
		op, err := d.UpdateInstanceState(instanceName, req, "")
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	// Base request
	req := api.InstanceRebuildPost{
		Source: api.InstanceSource{},
	}

	if parsed[0].BranchID == 1 {
		imgRemoteName := parsed[0].List[0].Get(conf.DefaultRemote)

		imgServer, imgInfo, err := getImgInfo(d, conf, imgRemoteName, remoteName, parsed[0].List[1].String, &req.Source)
		if err != nil {
			return err
		}

		if conf.Remotes[imgRemoteName].Protocol == "incus" {
			if imgInfo.Type != "virtual-machine" && current.Type == "virtual-machine" {
				return errors.New(i18n.G("Asked for a VM but image is of type container"))
			}
		}

		op, err := d.RebuildInstanceFromImage(imgServer, *imgInfo, instanceName, req)
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
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

		progress.Done("")
	} else {
		req.Source.Type = "none"
		op, err := d.RebuildInstance(instanceName, req)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	// If the instance was stopped, start it back up.
	if c.flagForce && current.StatusCode == api.Running {
		req := api.InstanceStatePut{
			Action: "start",
		}

		// Update the instance.
		op, err := d.UpdateInstanceState(instanceName, req, "")
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Quiet: c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	return nil
}
