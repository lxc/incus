//go:build windows

package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/i18n"
)

func (c *cmdWebui) run(cmd *cobra.Command, args []string) error {
	return errors.New(i18n.G("This command isn't supported on Windows"))
}
