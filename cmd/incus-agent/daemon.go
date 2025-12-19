package main

import (
	"sync"

	"github.com/lxc/incus/v6/internal/server/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers.
	events *events.Server

	// Paths.
	secretsLocation string

	// Agent config.
	Features map[string]bool

	// ContextID and port of the host socket server.
	serverCID         uint32
	serverPort        uint32
	serverCertificate string

	// The channel which is used to indicate that the agent was able to connect to the host.
	chConnected chan struct{}

	DevIncusRunning bool
	DevIncusMu      sync.Mutex
	DevIncusEnabled bool
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool, secretsLocation string) *Daemon {
	hostEvents := events.NewServer(debug, verbose, nil)

	return &Daemon{
		secretsLocation: secretsLocation,
		events:          hostEvents,
		chConnected:     make(chan struct{}),
	}
}
