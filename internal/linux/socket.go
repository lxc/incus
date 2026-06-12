package linux

import (
	"fmt"
	"net"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ListenUnix binds a unix socket, reaching the path through a short
// /proc/self/fd reference to the parent directory to handle long paths.
func ListenUnix(path string) (*net.UnixListener, error) {
	dir, file := filepath.Split(path)

	dirFD, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	defer func() { _ = unix.Close(dirFD) }()

	unixaddr, err := net.ResolveUnixAddr("unix", fmt.Sprintf("/proc/self/fd/%d/%s", dirFD, file))
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenUnix("unix", unixaddr)
	if err != nil {
		return nil, err
	}

	// The /proc/self/fd reference is no longer valid once the directory is
	// closed, so the caller is responsible for removing the socket path.
	listener.SetUnlinkOnClose(false)

	return listener, nil
}

// DialUnix connects to a unix socket, reaching the path through a short
// /proc/self/fd reference to the parent directory to handle long paths.
func DialUnix(path string) (*net.UnixConn, error) {
	dir, file := filepath.Split(path)

	dirFD, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	defer func() { _ = unix.Close(dirFD) }()

	unixaddr, err := net.ResolveUnixAddr("unix", fmt.Sprintf("/proc/self/fd/%d/%s", dirFD, file))
	if err != nil {
		return nil, err
	}

	return net.DialUnix("unix", nil, unixaddr)
}
