package main

import (
	"sync"

	"github.com/lxc/incus/incusd/events"
	"github.com/lxc/incus/incusd/vsock"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server

	// ContextID and port of the LXD VM socket server.
	serverCID         uint32
	serverPort        uint32
	serverCertificate string

	localCID uint32

	// The channel which is used to indicate that the lxd-agent was able to connect to LXD.
	chConnected chan struct{}

	devlxdRunning bool
	devlxdMu      sync.Mutex
	devlxdEnabled bool
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool) *Daemon {
	lxdEvents := events.NewServer(debug, verbose, nil)

	cid, _ := vsock.ContextID()

	return &Daemon{
		events:      lxdEvents,
		chConnected: make(chan struct{}),
		localCID:    cid,
	}
}
