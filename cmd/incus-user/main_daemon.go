package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/incus/util"
)

var mu sync.RWMutex
var connections uint64
var transactions uint64

type cmdDaemon struct{}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-user"
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	// Setup logger.
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)
	log.SetOutput(os.Stdout)

	// Connect to LXD.
	log.Debug("Connecting to LXD")
	client, err := incus.ConnectLXDUnix("", nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to LXD: %w", err)
	}

	// Validate LXD configuration.
	ok, err := lxdIsConfigured(client)
	if err != nil {
		return fmt.Errorf("Failed to check LXD configuration: %w", err)
	}

	if !ok {
		log.Info("Performing initial LXD configuration")
		err = lxdInitialConfiguration(client)
		if err != nil {
			return fmt.Errorf("Failed to apply initial LXD configuration: %w", err)
		}
	}

	// Disconnect from LXD.
	client.Disconnect()

	// Setup the unix socket.
	listeners := util.GetListeners(util.SystemdListenFDsStart)
	if len(listeners) > 1 {
		return fmt.Errorf("More than one socket-activation FD received")
	}

	var listener *net.UnixListener
	if len(listeners) == 1 {
		// Handle socket activation.
		unixListener, ok := listeners[0].(*net.UnixListener)
		if !ok {
			return fmt.Errorf("Socket-activation FD isn't a unix socket")
		}

		listener = unixListener

		// Automatically shutdown after inactivity.
		go func() {
			for {
				time.Sleep(30 * time.Second)

				// Check for active connections.
				mu.RLock()
				if connections > 0 {
					mu.RUnlock()
					continue
				}

				// Look for recent activity
				oldCount := transactions
				mu.RUnlock()

				time.Sleep(5 * time.Second)

				mu.RLock()
				if oldCount == transactions {
					mu.RUnlock()

					// Daemon has been inactive for 10s, exit.
					os.Exit(0)
				}

				mu.RUnlock()
			}
		}()
	} else {
		// Create our own socket.
		unixPath := "unix.socket"
		err := os.Remove(unixPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Failed to delete pre-existing unix socket: %w", err)
		}

		unixAddr, err := net.ResolveUnixAddr("unix", unixPath)
		if err != nil {
			return fmt.Errorf("Unable to resolve unix socket: %w", err)
		}

		server, err := net.ListenUnix("unix", unixAddr)
		if err != nil {
			return fmt.Errorf("Unable to setup unix socket: %w", err)
		}

		server.SetUnlinkOnClose(true)

		listener = server
	}

	// Start accepting requests.
	log.Info("Starting up the server")

	for {
		// Accept new connection.
		conn, err := listener.AcceptUnix()
		if err != nil {
			log.Error("Failed to accept new connection: %w", err)
			continue
		}

		go proxyConnection(conn)
	}
}
