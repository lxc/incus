package cliconfig

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

func getConfigDir() (string, error) {
	// Honor an explicit override.
	if os.Getenv("INCUS_CONF") != "" {
		return os.Getenv("INCUS_CONF"), nil
	}

	// Use the platform-specific user configuration directory.
	// (~/.config on Linux, ~/Library/Application Support on macOS, %AppData% on Windows)
	baseDir, err := os.UserConfigDir()
	if err != nil || baseDir == "" {
		// Fall back to the current user's home directory.
		usr, err := user.Current()
		if err != nil {
			return "", err
		}

		if usr.HomeDir == "" {
			return "", nil
		}

		baseDir = filepath.Join(usr.HomeDir, ".config")
	}

	return filepath.Join(baseDir, "incus"), nil
}

// migrateConfigDir moves the configuration from the legacy ~/.config/incus
// location to the platform-specific directory on first use.
func migrateConfigDir(configDir string) {
	// Nothing to migrate if the target already exists or an override is set.
	if util.PathExists(configDir) || os.Getenv("INCUS_CONF") != "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}

	legacyDir := filepath.Join(home, ".config", "incus")
	if legacyDir == configDir || !util.PathExists(legacyDir) {
		return
	}

	// Make sure the parent of the target exists, then move the data over.
	err = os.MkdirAll(filepath.Dir(configDir), 0o700)
	if err != nil {
		return
	}

	err = os.Rename(legacyDir, configDir)
	if err != nil {
		return
	}

	// On macOS, leave a symlink at the legacy location as ~/.config is
	// commonly used by CLI tools and users may expect to find it there.
	if runtime.GOOS == "darwin" {
		_ = os.Symlink(configDir, legacyDir)
	}
}

func getConfigPaths() (string, string, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return "", "", err
	}

	if configDir == "" {
		return "", "", nil
	}

	// Migrate any configuration from the legacy location.
	migrateConfigDir(configDir)

	configPath := os.ExpandEnv(filepath.Join(configDir, "config.yml"))

	return configPath, filepath.Dir(configPath), nil
}

// LoadConfig reads the configuration from the config path; if the path does
// not exist, it returns a default configuration; if the given path is empty
// it tries to determine automatically the configuration file to load.
func LoadConfig(path string) (*Config, error) {
	configDir := filepath.Dir(path)
	if path == "" {
		var err error
		path, configDir, err = getConfigPaths()
		if err != nil {
			return nil, err
		}
	}

	if path == "" || !util.PathExists(path) {
		return NewConfig(configDir, true), nil
	}

	// Open the config file
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to read the configuration file: %w", err)
	}

	// Decode the YAML document
	c := NewConfig(configDir, false)
	err = yaml.Load(content, c)
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
		err = yaml.Load(content, globalConf)
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

	defer logger.WarnOnError(f.Close, "Failed to close file")

	// Write the new config
	data, err := yaml.Dump(&conf, yaml.WithV2Defaults())
	if err != nil {
		return fmt.Errorf("Unable to marshal the configuration: %w", err)
	}

	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("Unable to write the configuration: %w", err)
	}

	return nil
}
