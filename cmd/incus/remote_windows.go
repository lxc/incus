//go:build windows

package main

import (
	"github.com/spf13/cobra"
)

type cmdRemoteProxy struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteProxy) command() *cobra.Command {
	return nil
}
