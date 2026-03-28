package main

import (
	"github.com/spf13/cobra"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdInit

	flagFreeze bool
}

func (c *cmdLaunch) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "launch [[<remote>:]<image>]"
	cmd.Short = "Create and start containers"
	cmd.RunE = c.run
	cmd.Flags().AddFlagSet(c.init.command().Flags())
	cmd.Flags().BoolVarP(&c.flagFreeze, "freeze", "F", false, "Freeze the container right after start")

	return cmd
}

func (c *cmdLaunch) run(cmd *cobra.Command, args []string) error {
	// Choose the image
	image := "images:debian/12"
	if len(args) > 0 {
		image = args[0]
	}

	// Run the test
	duration, err := launchContainers(c.global.srv, c.init.flagCount, c.global.flagParallel, image, c.init.flagPrivileged, true, c.flagFreeze)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
