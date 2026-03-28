package main

import (
	"github.com/spf13/cobra"
)

type cmdStart struct {
	global *cmdGlobal
}

func (c *cmdStart) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "start"
	cmd.Short = "Start containers"
	cmd.RunE = c.run

	return cmd
}

func (c *cmdStart) run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := getContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := startContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
