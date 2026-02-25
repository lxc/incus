//go:build linux

package main

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v2"

	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

// RunPreseed runs the actual command logic.
func (c *cmdAdminInit) RunPreseed(p *u.Parsed) (*api.InitPreseed, error) {
	// Read the YAML
	var bytes []byte
	var err error

	if p.Skipped || p.String == "-" {
		bytes, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to read from stdin: %w"), err)
		}
	} else {
		bytes, err = os.ReadFile(p.String)
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
