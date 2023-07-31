package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func (c *cmdInit) RunPreseed(cmd *cobra.Command, args []string, d lxd.InstanceServer) (*api.InitPreseed, error) {
	// Read the YAML
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from stdin: %w", err)
	}

	// Parse the YAML
	config := api.InitPreseed{}
	// Use strict checking to notify about unknown keys.
	err = yaml.UnmarshalStrict(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse the preseed: %w", err)
	}

	return &config, nil
}
