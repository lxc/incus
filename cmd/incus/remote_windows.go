//go:build windows

package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteProxy struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteProxy) Command() *cobra.Command {
	return nil
}
