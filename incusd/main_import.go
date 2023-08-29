package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type cmdImport struct {
	global *cmdGlobal
}

func (c *cmdImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "import"
	cmd.Short = `Command has been replaced with "recover"`
	cmd.Long = `Description:
  This command has been replaced with "recover". Please use that instead.
`
	cmd.RunE = c.Run
	return cmd
}

func (c *cmdImport) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf(`Command has been replaced with "recover"`)
}
