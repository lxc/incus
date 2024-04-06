//go:build linux && cgo

package endpoints

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// Bind to the given unix socket path.
func socketUnixListen(path string) (*net.UnixListener, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve socket address: %w", err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind socket: %w", err)
	}

	return listener, err
}

// CheckAlreadyRunning checks if the socket at the given path is already
// bound to a running process, and return an error if so.
//
//	FIXME: We should probably rather just try a regular unix socket
//		connection without using the client. However this is the way
//		this logic has historically behaved, so let's keep it like it
//		was.
func CheckAlreadyRunning(path string) error {
	// If socket activated, nothing to do
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err == nil {
		if pid == os.Getpid() {
			return nil
		}
	}

	// If there's no socket file at all, there's nothing to do.
	if !util.PathExists(path) {
		return nil
	}

	_, err = incus.ConnectIncusUnix(path, nil)

	// If the connection succeeded it means there's another daemon running.
	if err == nil {
		return fmt.Errorf("Incus is already running")
	}

	return nil
}

// Remove any stale socket file at the given path.
func socketUnixRemoveStale(path string) error {
	// If there's no socket file at all, there's nothing to do.
	if !util.PathExists(path) {
		return nil
	}

	logger.Debugf("Detected stale unix socket, deleting")
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("could not delete stale local socket: %w", err)
	}

	return nil
}

// Change the file mode of the given unix socket file,.
func socketUnixSetPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("cannot set permissions on local socket: %w", err)
	}

	return nil
}

// Change the ownership of the given unix socket file,.
func socketUnixSetOwnership(path string, groupName string) error {
	var gid int
	var err error

	if groupName != "" {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			return fmt.Errorf("cannot get group ID of '%s': %w", groupName, err)
		}

		gid, err = strconv.Atoi(g.Gid)
		if err != nil {
			return err
		}
	} else {
		gid = os.Getgid()
	}

	err = os.Chown(path, os.Getuid(), gid)
	if err != nil {
		return fmt.Errorf("cannot change ownership on local socket: %w", err)
	}

	return nil
}
