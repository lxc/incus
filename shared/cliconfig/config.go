package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/lxc/incus/v6/shared/util"
)

// Config holds settings to be used by a client or daemon.
type Config struct {
	// DefaultRemote holds the remote daemon name from the Remotes map
	// that the client should communicate with by default
	DefaultRemote string `yaml:"default-remote"`

	// Remotes defines a map of remote daemon names to the details for
	// communication with the named daemon
	Remotes map[string]Remote `yaml:"remotes"`

	// Command line aliases for `incus`
	Aliases map[string]string `yaml:"aliases"`

	// Configuration directory
	ConfigDir string `yaml:"-"`

	// The UserAgent to pass for all queries
	UserAgent string `yaml:"-"`

	// PromptPassword is a helper function used when encountering an encrypted key
	PromptPassword func(filename string) (string, error) `yaml:"-"`

	// ProjectOverride allows overriding the default project
	ProjectOverride string `yaml:"-"`

	// OIDC tokens
	oidcTokens map[string]*oidc.Tokens[*oidc.IDTokenClaims]
}

// GlobalConfigPath returns a joined path of the global configuration directory and passed arguments.
func (c *Config) GlobalConfigPath(paths ...string) string {
	configDir := "/etc/incus"
	if os.Getenv("INCUS_GLOBAL_CONF") != "" {
		configDir = os.Getenv("INCUS_GLOBAL_CONF")
	}

	path := []string{configDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// ConfigPath returns a joined path of the configuration directory and passed arguments.
func (c *Config) ConfigPath(paths ...string) string {
	path := []string{c.ConfigDir}
	path = append(path, paths...)

	return filepath.Join(path...)
}

// CookiesPath returns the path for the remote's cookie jar.
func (c *Config) CookiesPath(remote string) string {
	return c.ConfigPath("jars", remote)
}

// ServerCertPath returns the path for the remote's server certificate.
func (c *Config) ServerCertPath(remote string) string {
	if c.Remotes[remote].Global {
		return c.GlobalConfigPath("servercerts", fmt.Sprintf("%s.crt", remote))
	}

	return c.ConfigPath("servercerts", fmt.Sprintf("%s.crt", remote))
}

// OIDCTokenPath returns the path for the remote's OIDC tokens.
func (c *Config) OIDCTokenPath(remote string) string {
	return c.ConfigPath("oidctokens", fmt.Sprintf("%s.json", remote))
}

// SaveOIDCTokens saves OIDC tokens to disk.
func (c *Config) SaveOIDCTokens() {
	tokenParentPath := c.ConfigPath("oidctokens")

	if !util.PathExists(tokenParentPath) {
		_ = os.MkdirAll(tokenParentPath, 0755)
	}

	for remote, tokens := range c.oidcTokens {
		tokenPath := c.OIDCTokenPath(remote)
		data, _ := json.Marshal(tokens)
		_ = os.WriteFile(tokenPath, data, 0600)
	}
}

// NewConfig returns a Config, optionally using default remotes.
func NewConfig(configDir string, defaults bool) *Config {
	var config *Config
	if defaults {
		config = DefaultConfig()
	} else {
		config = &Config{}
	}

	config.ConfigDir = configDir

	return config
}
