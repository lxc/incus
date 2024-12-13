package main

/*
#include "config.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <signal.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#include <limits.h>

#include "incus.h"
#include "memory_utils.h"

void forkfile(void)
{
	int ns_fd = -EBADF, pidfd = -EBADF, rootfs_fd = -EBADF;
	char *listenfd = NULL;
	pid_t pid = 0;

	// Check that we're root.
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkfile requires root privileges\n");
		_exit(1);
	}

	// Check the first argument.
	listenfd = advance_arg(false);
	if (listenfd == NULL)
		return;

	if (strcmp(listenfd, "--") == 0)
		listenfd = advance_arg(false);

	if (listenfd == NULL || (strcmp(listenfd, "--help") == 0 || strcmp(listenfd, "--version") == 0 || strcmp(listenfd, "-h") == 0))
		return;

	// Get the container rootfs.
	rootfs_fd = atoi(advance_arg(true));

	// Get the container PID.
	pidfd = atoi(advance_arg(true));
	pid = atoi(advance_arg(true));

	if (pid > 0 || pidfd >= 0) {
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0) {
			_exit(1);
		}
	}

	// Attach to the container.
	if (ns_fd >= 0) {
		attach_userns_fd(ns_fd);

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			error("error: setns");
			_exit(1);
		}
	} else {
		if (fchdir(rootfs_fd) < 0) {
			error("error: fchdir");
			_exit(1);
		}

		if (chroot(".") < 0) {
			error("error: chroot");
			_exit(1);
		}

		if (chdir("/") < 0) {
			error("error: chdir");
			_exit(1);
		}
	}
}
*/
import "C"

import (
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

type cmdForkfile struct {
	global *cmdGlobal
}

func (c *cmdForkfile) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkfile <listen fd> <rootfs fd> <PIDFd> <PID>"
	cmd.Short = "Perform container file operations"
	cmd.Long = `Description:
  Perform container file operations

  This spawns a daemon inside of the instance's filesystem which can
  then receive command over a simple SFTP API operating on the provided
  listen fd.

  The command can be called with PID and PIDFd set to 0 to just operate on the rootfs fd.
  In such cases, it's the responsibility of the caller to handle any kind of userns shifting.
`
	cmd.Hidden = true
	cmd.Args = cobra.ExactArgs(4)
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdForkfile) Run(cmd *cobra.Command, args []string) error {
	var mu sync.RWMutex
	var connections uint64
	var transactions uint64

	// Convert the listener FD number.
	listenFD, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}

	// Setup listener.
	listenerFile := os.NewFile(uintptr(listenFD), "forkfile.sock")
	listener, err := net.FileListener(listenerFile)
	if err != nil {
		return err
	}

	defer func() { _ = listener.Close() }()

	// Convert the rootfs FD number.
	rootfsFD, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}

	// Automatically shutdown after inactivity.
	go func() {
		for {
			time.Sleep(10 * time.Second)

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

	// Signal handler.
	go func() {
		// Wait for SIGINT.
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, unix.SIGINT)
		<-sigs

		// Prevent new connections.
		_ = listener.Close()

		// Wait for connections to be gone and exit.
		for {
			mu.RLock()
			if connections == 0 {
				mu.RUnlock()
				break
			}

			mu.RUnlock()

			time.Sleep(time.Second)
		}

		os.Exit(0)
	}()

	// Connection handler.
	for {
		// Accept new connection.
		conn, err := listener.Accept()
		if err != nil {
			continue
		}

		go func(conn net.Conn) {
			defer func() {
				_ = conn.Close()

				mu.Lock()
				connections -= 1
				mu.Unlock()
			}()

			// Increase counters.
			mu.Lock()
			transactions += 1
			connections += 1
			mu.Unlock()

			// Spawn the server.
			server, err := sftp.NewServer(conn, sftp.WithAllocator())
			if err != nil {
				return
			}

			_ = server.Serve()

			// Sync the filesystem.
			_ = unix.Syncfs(int(rootfsFD))
		}(conn)
	}
}
