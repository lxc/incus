package config

// LocalRemote is the default local remote (over the LXD unix socket).
var LocalRemote = Remote{
	Addr:   "unix://",
	Static: true,
	Public: false,
}

// ImagesRemote is the community image server (over simplestreams).
var ImagesRemote = Remote{
	Addr:     "https://images.linuxcontainers.org",
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuRemote is the Ubuntu image server (over simplestreams).
var UbuntuRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/releases",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuDailyRemote is the Ubuntu daily image server (over simplestreams).
var UbuntuDailyRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/daily",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// StaticRemotes is the list of remotes which can't be removed.
var StaticRemotes = map[string]Remote{
	"local":        LocalRemote,
	"ubuntu":       UbuntuRemote,
	"ubuntu-daily": UbuntuDailyRemote,
}

// DefaultRemotes is the list of default remotes.
var DefaultRemotes = map[string]Remote{
	"images":       ImagesRemote,
	"local":        LocalRemote,
	"ubuntu":       UbuntuRemote,
	"ubuntu-daily": UbuntuDailyRemote,
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	// Duplicate remotes from DefaultRemotes.
	defaultRoutes := make(map[string]Remote, len(DefaultRemotes))
	for k, v := range DefaultRemotes {
		defaultRoutes[k] = v
	}

	return &Config{
		Remotes:       defaultRoutes,
		Aliases:       make(map[string]string),
		DefaultRemote: "local",
	}
}
