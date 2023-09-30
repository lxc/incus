package main

import (
	"sync"

	"github.com/lxc/incus/internal/server/events"
	"github.com/lxc/incus/internal/server/vsock"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server

	// ContextID and port of the host socket server.
	serverCID         uint32
	serverPort        uint32
	serverCertificate string

	localCID uint32

	// The channel which is used to indicate that the agent was able to connect to the host.
	chConnected chan struct{}

	DevIncusRunning bool
	DevIncusMu      sync.Mutex
	DevIncusEnabled bool
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool) *Daemon {
	hostEvents := events.NewServer(debug, verbose, nil)

	cid, _ := vsock.ContextID()

	return &Daemon{
		events:      hostEvents,
		chConnected: make(chan struct{}),
		localCID:    cid,
	}
}
