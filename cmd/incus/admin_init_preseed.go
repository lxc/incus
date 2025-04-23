//go:build linux

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/goccy/go-yaml"

	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

// RunPreseed runs the actual command logic.
func (c *cmdAdminInit) RunPreseed() (*api.InitPreseed, error) {
	// Read the YAML
	bytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Failed to read from stdin: %w"), err)
	}

	// Parse the YAML
	config := api.InitPreseed{}
	// Use strict checking to notify about unknown keys.
	err = yaml.UnmarshalWithOptions(bytes, &config, yaml.Strict())
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Failed to parse the preseed: %w"), err)
	}

	return &config, nil
}
