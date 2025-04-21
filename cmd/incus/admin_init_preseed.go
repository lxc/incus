//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// RunPreseed runs the actual command logic.
func (c *cmdAdminInit) RunPreseed() (*api.InitPreseed, error) {
	// Parse the YAML
	config := api.InitPreseed{}

	err := util.YAMLUnmarshalStrict(os.Stdin, &config)
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Failed to parse the preseed: %w"), err)
	}

	return &config, nil
}
