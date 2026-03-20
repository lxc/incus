package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

// Start.
type cmdStart struct {
	global *cmdGlobal
	action *cmdAction
}

var cmdActionUsage = u.Usage{u.Either(u.Instance.Remote().List(1), u.Sequence(u.Flag("all"), u.RemoteColon.List(0)))}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStart) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("start")
	cmd.Use = cli.U("start", cmdActionUsage...)
	cmd.Short = i18n.G("Start instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Start instances`))

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

// Pause.
type cmdPause struct {
	global *cmdGlobal
	action *cmdAction
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdPause) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("pause")
	cmd.Use = cli.U("pause", cmdActionUsage...)
	cmd.Short = i18n.G("Pause instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Pause instances`))
	cmd.Aliases = []string{"freeze"}

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

// Resume.
type cmdResume struct {
	global *cmdGlobal
	action *cmdAction
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdResume) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("resume")
	cmd.Use = cli.U("resume", cmdActionUsage...)
	cmd.Short = i18n.G("Resume instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Resume instances`))
	cmd.Aliases = []string{"unfreeze"}

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

// Restart.
type cmdRestart struct {
	global *cmdGlobal
	action *cmdAction
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRestart) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("restart")
	cmd.Use = cli.U("restart", cmdActionUsage...)
	cmd.Short = i18n.G("Restart instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Restart instances`))

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

// Stop.
type cmdStop struct {
	global *cmdGlobal
	action *cmdAction
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStop) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("stop")
	cmd.Use = cli.U("stop", cmdActionUsage...)
	cmd.Short = i18n.G("Stop instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Stop instances`))

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

type cmdAction struct {
	global *cmdGlobal

	flagAll       bool
	flagConsole   string
	flagForce     bool
	flagStateful  bool
	flagStateless bool
	flagTimeout   int
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAction) Command(action string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.Run

	cmd.Flags().BoolVar(&c.flagAll, "all", false, i18n.G("Run against all instances"))

	switch action {
	case "stop":
		cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Store the instance state"))
	case "start":
		cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Ignore the instance state"))
	}

	if slices.Contains([]string{"start", "restart", "stop"}, action) {
		cmd.Flags().StringVar(&c.flagConsole, "console", "", i18n.G("Immediately attach to the console")+"``")
		cmd.Flags().Lookup("console").NoOptDefVal = "console"
	}

	if slices.Contains([]string{"restart", "stop"}, action) {
		cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the instance to stop"))
		cmd.Flags().IntVar(&c.flagTimeout, "timeout", -1, i18n.G("Time to wait for the instance to shutdown cleanly")+"``")
	}

	return cmd
}

// doActionAll is a method of the cmdAction structure. It performs a specified action on all instances of a remote resource.
// It ensures that flags and parameters are appropriately set, and handles any errors that may occur during the process.
func (c *cmdAction) doActionAll(action string, d incus.InstanceServer) error {
	// Pause is called freeze, resume is called unfreeze.
	switch action {
	case "pause":
		action = "freeze"
	case "resume":
		action = "unfreeze"
	}

	// Only store state if asked to.
	state := action == "stop" && c.flagStateful

	req := api.InstancesPut{
		State: &api.InstanceStatePut{
			Action:   action,
			Timeout:  c.flagTimeout,
			Force:    c.flagForce,
			Stateful: state,
		},
	}

	// Update all instances.
	op, err := d.UpdateInstances(req, "")
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

	progress.Done("")

	return nil
}

// doAction is a method of the cmdAction structure. It carries out a specified action on an instance,
// using a given config and instance name. It manages state changes, flag checks, error handling and console attachment.
func (c *cmdAction) doAction(action string, conf *config.Config, p *u.Parsed) error {
	d := p.RemoteServer
	instanceName := p.RemoteObject.String

	state := false

	// Pause is called freeze
	if action == "pause" {
		action = "freeze"
	}

	// Resume is called unfreeze
	if action == "resume" {
		action = "unfreeze"
	}

	// Only store state if asked to
	if action == "stop" && c.flagStateful {
		state = true
	}

	if action == "stop" && c.flagForce && c.flagConsole != "" {
		return errors.New(i18n.G("--console can't be used while forcing instance shutdown"))
	}

	if action == "start" {
		current, _, err := d.GetInstance(instanceName)
		if err != nil {
			return err
		}

		// "start" for a frozen instance means "unfreeze"
		if current.StatusCode == api.Frozen {
			action = "unfreeze"
		}

		// Always restore state (if present) unless asked not to
		if action == "start" && current.Stateful && !c.flagStateless {
			state = true
		}
	}

	req := api.InstanceStatePut{
		Action:   action,
		Timeout:  c.flagTimeout,
		Force:    c.flagForce,
		Stateful: state,
	}

	op, err := d.UpdateInstanceState(instanceName, req, "")
	if err != nil {
		return err
	}

	if action == "stop" && c.flagConsole != "" {
		// Handle console attach
		console := cmdConsole{}
		console.global = c.global
		console.flagType = c.flagConsole
		return console.console(d, instanceName)
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

// Run is a method of the cmdAction structure that implements the execution logic for the given Cobra command.
// It handles actions on instances (single or all) and manages error handling, console flag restrictions, and batch operations.
func (c *cmdAction) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdActionUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	action := cmd.Name()

	type batchEntry struct {
		parsed *u.Parsed
		err    error
	}

	var batch []batchEntry
	if c.flagAll {
		if parsed[0].BranchID != 1 {
			return errors.New(i18n.G("Both --all and instance name given"))
		}

		parsedRemotes := parsed[0].List[1].List

		// If no remote passed, use current default.
		if len(parsedRemotes) == 0 {
			p, err := u.ParseDefault(u.RemoteColonOpt, c.global.conf)
			if err != nil {
				return err
			}

			parsedRemotes = append(parsedRemotes, p)
		}

		for _, p := range parsedRemotes {
			d := p.RemoteServer

			// See if we can use the bulk API.
			if d.HasExtension("instance_bulk_state_change") {
				err = c.doActionAll(action, d)
				if err != nil {
					return fmt.Errorf("%s: %w", p.RemoteName, err)
				}

				continue
			}

			instances, err := d.GetInstances(api.InstanceTypeAny)
			if err != nil {
				return err
			}

			for _, instance := range instances {
				switch action {
				case "start":
					if instance.StatusCode == api.Running {
						continue
					}

				case "stop":
					if instance.StatusCode == api.Stopped {
						continue
					}
				}

				reparsed := *p
				reparsed.RemoteObject = u.ParseString(instance.Name)
				batch = append(batch, batchEntry{&reparsed, nil})
			}
		}
	} else {
		for _, p := range parsed[0].List {
			batch = append(batch, batchEntry{p, nil})
		}
	}

	if c.flagConsole != "" {
		if c.flagAll {
			return errors.New(i18n.G("--console can't be used with --all"))
		}

		if len(batch) > 1 {
			return errors.New(i18n.G("--console only works with a single instance"))
		}
	}

	// Run the action for every listed instance
	var wg sync.WaitGroup
	wg.Add(len(batch))
	for i := range batch {
		go func(entry *batchEntry) {
			defer wg.Done()
			entry.err = c.doAction(action, c.global.conf, entry.parsed)
		}(&batch[i])
	}

	wg.Wait()

	// Single instance is easy
	if len(batch) == 1 {
		return batch[0].err
	}

	// Do fancier rendering for batches
	success := true

	for _, entry := range batch {
		if entry.err == nil {
			continue
		}

		success = false
		msg := fmt.Sprintf(i18n.G("error: %v"), entry.err)
		for _, line := range strings.Split(msg, "\n") {
			fmt.Fprintf(os.Stderr, "%s: %s\n", formatRemote(c.global.conf, entry.parsed), line)
		}
	}

	if !success {
		fmt.Fprintln(os.Stderr, "")
		return fmt.Errorf(i18n.G("Some instances failed to %s"), action)
	}

	return nil
}
