package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdCreate

	flagConsole string
}

func (c *cmdLaunch) command() *cobra.Command {
	cmd := c.init.command()
	cmd.Use = cli.U("launch", cmdCreateUsage...)
	cmd.Short = i18n.G("Create and start instances from images")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create and start instances from images`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus launch images:debian/12 u1

incus launch images:debian/12 u1 < config.yaml
    Create and start a container with configuration from config.yaml

incus launch images:debian/12 u2 -t aws:t2.micro
    Create and start a container using the same size as an AWS t2.micro (1 vCPU, 1GiB of RAM)

incus launch images:debian/12 v1 --vm -c limits.cpu=4 -c limits.memory=4GiB
    Create and start a virtual machine with 4 vCPUs and 4GiB of RAM

incus launch images:debian/12 v2 --vm -d root,size=50GiB -d root,io.bus=nvme
    Create and start a virtual machine, overriding the disk size and bus`))
	cmd.Hidden = false

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagConsole, "console", "", i18n.G("Immediately attach to the console")+"``")
	cmd.Flags().Lookup("console").NoOptDefVal = "console"

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdLaunch) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	parsed, err := cmdCreateUsage.Parse(conf, cmd, args)
	if err != nil {
		return err
	}

	// Call the matching code from init
	p, err := c.init.create(conf, parsed, true)
	if err != nil {
		return err
	}

	d := p.RemoteServer
	instanceName := p.RemoteObject.String

	// Check if the instance was started by the server.
	if d.HasExtension("instance_create_start") {
		// Handle console attach
		if c.flagConsole != "" {
			console := cmdConsole{}
			console.global = c.global
			console.flagType = c.flagConsole

			consoleErr := console.console(d, instanceName)
			if consoleErr != nil {
				// Check if still running.
				state, _, err := d.GetInstanceState(instanceName)
				if err != nil {
					return err
				}

				if state.StatusCode != api.Stopped {
					return consoleErr
				}

				console.flagShowLog = true
				return console.console(d, instanceName)
			}
		}

		return nil
	}

	// Start the instance
	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Starting %s")+"\n", formatRemote(conf, p))
	}

	req := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

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

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")

		projectArg := ""
		if conf.ProjectOverride != "" && conf.ProjectOverride != api.ProjectDefaultName {
			projectArg = " --project " + conf.ProjectOverride
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `incus info --show-log %s%s` for more info"), err, formatRemote(conf, p), projectArg)
	}

	progress.Done("")

	// Handle console attach
	if c.flagConsole != "" {
		console := cmdConsole{}
		console.global = c.global
		console.flagType = c.flagConsole

		consoleErr := console.console(d, instanceName)
		if consoleErr != nil {
			// Check if still running.
			state, _, err := d.GetInstanceState(instanceName)
			if err != nil {
				return err
			}

			if state.StatusCode != api.Stopped {
				return consoleErr
			}

			console.flagShowLog = true
			return console.console(d, instanceName)
		}
	}

	return nil
}
