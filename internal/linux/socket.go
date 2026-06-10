package linux

import (
	"fmt"
	"net"
	"path/filepath"

	"golang.org/x/sys/unix"
)

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
