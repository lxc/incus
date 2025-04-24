package main

import (
	"github.com/spf13/cobra"
)

type cmdInit struct {
	global *cmdGlobal

	flagCount      int
	flagPrivileged bool
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init [[<remote>:]<image>]"
	cmd.Short = "Create containers"
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagCount, "count", "C", 1, "Number of containers to create"+"``")
	cmd.Flags().BoolVar(&c.flagPrivileged, "privileged", false, "Use privileged containers")

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Choose the image
	image := "images:debian/12"
	if len(args) > 0 {
		image = args[0]
	}

	// Run the test
	duration, err := LaunchContainers(c.global.srv, c.flagCount, c.global.flagParallel, image, c.flagPrivileged, false, false)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
