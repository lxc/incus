//go:build linux

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

// RunPreseed runs the actual command logic.
func (c *cmdAdminInit) RunPreseed(cmd *cobra.Command, args []string) (*api.InitPreseed, error) {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return nil, err
	}

	// Read the YAML
	var bytes []byte

	if len(args) == 0 {
		bytes, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to read from stdin: %w"), err)
		}
	} else {
		bytes, err = os.ReadFile(args[0])
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to read from file: %w"), err)
		}
	}

	// Parse the YAML
	config := api.InitPreseed{}
	// Use strict checking to notify about unknown keys.
	err = yaml.UnmarshalStrict(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Failed to parse the preseed: %w"), err)
	}

	return &config, nil
}
