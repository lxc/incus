package main

import (
	"github.com/spf13/cobra"
)

type cmdStart struct {
	global *cmdGlobal
}

func (c *cmdStart) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "start"
	cmd.Short = "Start containers"
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStart) Run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := GetContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := StartContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
