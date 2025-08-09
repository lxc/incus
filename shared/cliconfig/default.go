package cliconfig

import (
	"github.com/lxc/incus/v6/shared/util"
)

// LocalRemote is the default local remote (over the unix socket).
var LocalRemote = Remote{
	Addr:     "unix://",
	Static:   true,
	Public:   false,
	Protocol: "incus",
}

// ImagesRemote is the community image server (over simplestreams).
var ImagesRemote = Remote{
	Addr:     "https://images.linuxcontainers.org",
	Public:   true,
	Protocol: "simplestreams",
}

// StaticRemotes is the list of remotes which can't be removed.
var StaticRemotes = map[string]Remote{
	"local": LocalRemote,
}

// DefaultRemotes is the list of default remotes.
var DefaultRemotes = map[string]Remote{
	"images": ImagesRemote,
	"local":  LocalRemote,
}

// DefaultSettings are the configurations for the Config Struct.
type DefaultSettings struct {
	// Default flag format for list commands.
	ListFormat string `yaml:"list_format"`

	// Preferred console type.
	ConsoleType string `yaml:"console_type"`

	// Alternative SPICE command (SOCKET will be replaced by the socket path).
	ConsoleSpiceCommand string `yaml:"console_spice_command"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Remotes:       util.CloneMap(DefaultRemotes),
		Aliases:       make(map[string]string),
		DefaultRemote: "local",
	}
}
