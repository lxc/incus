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

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Remotes:       util.CloneMap(DefaultRemotes),
		Aliases:       make(map[string]string),
		DefaultRemote: "local",
	}
}
