package main

import (
	"errors"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

type agentConfig struct {
	Features map[string]bool `yaml:"features"`
}

func loadAgentConfig(d *Daemon) error {
	data, err := os.ReadFile(osAgentConfigPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	cfg := agentConfig{}
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return err
	}

	d.Features = cfg.Features

	return nil
}
