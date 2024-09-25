package cliconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// LoadConfig reads the configuration from the config path; if the path does
// not exist, it returns a default configuration.
func LoadConfig(path string) (*Config, error) {
	// Open the config file
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to read the configuration file: %w", err)
	}

	// Decode the YAML document
	c := NewConfig(filepath.Dir(path), false)
	err = yaml.Unmarshal(content, &c)
	if err != nil {
		return nil, fmt.Errorf("Unable to decode the configuration: %w", err)
	}

	for k, r := range c.Remotes {
		if !r.Public && r.AuthType == "" {
			r.AuthType = api.AuthenticationMethodTLS
			c.Remotes[k] = r
		}
	}

	// Apply the global (system-wide) remotes
	globalConf := NewConfig("", false)
	content, err = os.ReadFile(globalConf.GlobalConfigPath("config.yml"))
	if err == nil {
		err = yaml.Unmarshal(content, &globalConf)
		if err != nil {
			return nil, fmt.Errorf("Unable to decode the configuration: %w", err)
		}

		for k, r := range globalConf.Remotes {
			_, ok := c.Remotes[k]
			if !ok {
				r.Global = true
				c.Remotes[k] = r
			}
		}
	}

	// Set default values
	if c.Remotes == nil {
		c.Remotes = make(map[string]Remote)
	}

	// Apply the static remotes
	for k, v := range StaticRemotes {
		if c.Remotes[k].Project != "" {
			v.Project = c.Remotes[k].Project
		}

		c.Remotes[k] = v
	}

	// Fill in defaults.
	for k, r := range c.Remotes {
		if r.Protocol == "" {
			r.Protocol = "incus"
			c.Remotes[k] = r
		}
	}

	// If the environment specifies a remote this takes priority over what
	// is defined in the configuration
	envDefaultRemote := os.Getenv("INCUS_REMOTE")
	if len(envDefaultRemote) > 0 {
		c.DefaultRemote = envDefaultRemote
	} else if c.DefaultRemote == "" {
		c.DefaultRemote = DefaultConfig().DefaultRemote
	}

	return c, nil
}

// SaveConfig writes the provided configuration to the config file.
func (c *Config) SaveConfig(path string) error {
	// Create a new copy for the config file
	conf := Config{}
	err := util.DeepCopy(c, &conf)
	if err != nil {
		return fmt.Errorf("Unable to copy the configuration: %w", err)
	}

	// Remove the global remotes
	for k, v := range c.Remotes {
		if v.Global {
			delete(conf.Remotes, k)
		}
	}

	defaultRemote := DefaultConfig().DefaultRemote

	// Remove the static remotes
	for k := range StaticRemotes {
		if k == defaultRemote {
			continue
		}

		delete(conf.Remotes, k)
	}

	// Create the config file (or truncate an existing one)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Unable to create the configuration file: %w", err)
	}

	defer func() { _ = f.Close() }()

	// Write the new config
	data, err := yaml.Marshal(conf)
	if err != nil {
		return fmt.Errorf("Unable to marshal the configuration: %w", err)
	}

	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("Unable to write the configuration: %w", err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("Unable to close the configuration file: %w", err)
	}

	return nil
}
