package main

import (
	"github.com/spf13/cobra"
)

type cmdStop struct {
	global *cmdGlobal
}

func (c *cmdStop) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "stop"
	cmd.Short = "Stop containers"
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStop) Run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := GetContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := StopContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
