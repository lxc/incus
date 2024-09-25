//go:build linux

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

func (c *cmdAdminInit) RunPreseed(cmd *cobra.Command, args []string, d incus.InstanceServer) (*api.InitPreseed, error) {
	// Read the YAML
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Failed to read from stdin: %w"), err)
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
