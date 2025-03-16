package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/linux"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
)

var (
	mu           sync.RWMutex
	connections  uint64
	transactions uint64
)

var projectNames []string

type cmdDaemon struct {
	flagGroup string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "incus-user"
	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagGroup, "group", "", "The group of users that will be allowed to talk to incus-user"+"``")

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Create storage.
	err := os.MkdirAll(internalUtil.VarPath("users"), 0o700)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("Couldn't create storage: %w", err)
	}

	// Connect.
	logger.Debug("Connecting to the daemon")
	client, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to the daemon: %w", err)
	}

	cinfo, err := client.GetConnectionInfo()
	if err != nil {
		return fmt.Errorf("Failed to obtain connection info: %w", err)
	}

	// Keep track of the socket path we used to successfully connect to the server
	serverUnixPath := cinfo.SocketPath

	// Validate the configuration.
	ok, err := serverIsConfigured(client)
	if err != nil {
		return fmt.Errorf("Failed to check the configuration: %w", err)
	}

	if !ok {
		logger.Info("Performing initial configuration")
		err = serverInitialConfiguration(client)
		if err != nil {
			return fmt.Errorf("Failed to apply initial configuration: %w", err)
		}
	}

	// Pull the list of projects.
	projectNames, err = client.GetProjectNames()
	if err != nil {
		return fmt.Errorf("Failed to pull project list: %w", err)
	}

	// Disconnect.
	client.Disconnect()

	// Setup the unix socket.
	listeners := linux.GetSystemdListeners(linux.SystemdListenFDsStart)
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
		unixPath := internalUtil.VarPath("unix.socket.user")
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

		err = os.Chmod(unixPath, 0o660)
		if err != nil {
			return fmt.Errorf("Unable to set socket permissions: %w", err)
		}

		if c.flagGroup != "" {
			g, err := user.LookupGroup(c.flagGroup)
			if err != nil {
				return fmt.Errorf("Cannot get group ID of '%s': %w", c.flagGroup, err)
			}

			gid, err := strconv.Atoi(g.Gid)
			if err != nil {
				return err
			}

			err = os.Chown(unixPath, os.Getuid(), gid)
			if err != nil {
				return fmt.Errorf("Cannot change ownership on local socket: %w", err)
			}
		}

		server.SetUnlinkOnClose(true)

		listener = server
	}

	// Start accepting requests.
	logger.Info("Starting up the server")

	for {
		// Accept new connection.
		conn, err := listener.AcceptUnix()
		if err != nil {
			logger.Errorf("Failed to accept new connection: %v", err)
			continue
		}

		go proxyConnection(conn, serverUnixPath)
	}
}
