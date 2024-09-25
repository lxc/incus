package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
)

// Rebuild.
type cmdRebuild struct {
	global    *cmdGlobal
	flagEmpty bool
	flagForce bool
}

func (c *cmdRebuild) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rebuild", i18n.G("[<remote>:]<image> [<remote>:]<instance>"))
	cmd.Short = i18n.G("Rebuild instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Wipe the instance root disk and re-initialize. The original image is used to re-initialize the instance if a different image or --empty is not specified.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagEmpty, "empty", false, i18n.G("Rebuild as an empty instance"))
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("If an instance is running, stop it and then rebuild it"))

	return cmd
}

func (c *cmdRebuild) rebuild(conf *config.Config, args []string) error {
	var name, image, remote, iremote string
	var err error

	if len(args) > 0 {
		if len(args) == 1 {
			remote, name, err = conf.ParseRemote(args[0])
			if err != nil {
				return err
			}
		} else if len(args) == 2 {
			iremote, image, err = conf.ParseRemote(args[0])
			if err != nil {
				return err
			}

			remote, name, err = conf.ParseRemote(args[1])
			if err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	if c.flagEmpty {
		if len(args) > 1 {
			return fmt.Errorf(i18n.G("--empty cannot be combined with an image name"))
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// We are not rebuilding just a snapshot but an instance
	if strings.Contains(name, instance.SnapshotDelimiter) {
		return fmt.Errorf(i18n.G("Instance snapshots cannot be rebuilt: %s"), name)
	}

	current, _, err := d.GetInstance(name)
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
		op, err := d.UpdateInstanceState(name, req, "")
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

	if !c.flagEmpty {
		if image == "" && iremote == "" {
			return fmt.Errorf(i18n.G("You need to specify an image name or use --empty"))
		}

		iremote, image := guessImage(conf, d, remote, iremote, image)
		imgRemote, imgInfo, err := getImgInfo(d, conf, iremote, remote, image, &req.Source)
		if err != nil {
			return err
		}

		if conf.Remotes[iremote].Protocol == "incus" {
			if imgInfo.Type != "virtual-machine" && current.Type == "virtual-machine" {
				return fmt.Errorf(i18n.G("Asked for a VM but image is of type container"))
			}
		}

		op, err := d.RebuildInstanceFromImage(imgRemote, *imgInfo, name, req)
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
		// This is a rebuild as an empty instance
		if image != "" || iremote != "" {
			return fmt.Errorf(i18n.G("Can't use an image with --empty"))
		}

		req.Source.Type = "none"
		op, err := d.RebuildInstance(name, req)
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
		op, err := d.UpdateInstanceState(name, req, "")
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

func (c *cmdRebuild) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	if len(args) == 0 {
		_ = cmd.Usage()
		return nil
	}

	err := c.rebuild(conf, args)
	if err != nil {
		return err
	}

	return nil
}
