package main

import (
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	EnabledFeatures []string `yaml:"enabled_features"`
}

func getConfigFilePath() string {
	switch runtime.GOOS {
	case "linux":
		return "/etc/incus-agent.yml"
	case "windows":
		return `C:\ProgramData\Incus-Agent\incus-agent.yml`
	case "darwin":
		return "/usr/local/etc/incus-agent.yml"
	default:
		return "/etc/incus-agent.yml"
	}
}

func loadAgentConfig() (map[string]bool, error) {
	path := getConfigFilePath()

	data, err := os.ReadFile(path)
	if err != nil {
		// File does not exist → return all enabled
		return map[string]bool{
			"info":    true,
			"metrics": true,
			"files":   true,
			"mounts":  true,
			"exec":    true,
		}, nil
	}

	cfg := AgentConfig{}
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}

	// If enabled_features not defined, enable all
	if len(cfg.EnabledFeatures) == 0 {
		return map[string]bool{
			"info":    true,
			"metrics": true,
			"files":   true,
			"mounts":  true,
			"exec":    true,
		}, nil
	}

	enabled := map[string]bool{}
	for _, f := range cfg.EnabledFeatures {
		enabled[f] = true
	}

	return enabled, nil
}
