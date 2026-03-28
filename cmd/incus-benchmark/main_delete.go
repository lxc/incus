package main

import (
	"github.com/spf13/cobra"
)

type cmdDelete struct {
	global *cmdGlobal
}

func (c *cmdDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "delete"
	cmd.Short = "Delete containers"
	cmd.RunE = c.run

	return cmd
}

func (c *cmdDelete) run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := getContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := deleteContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
