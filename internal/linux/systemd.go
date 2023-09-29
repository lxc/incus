package linux

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// GetSystemdListeners returns the socket-activated network listeners, if any.
//
// The 'start' parameter must be SystemdListenFDsStart, except in unit tests,
// see the docstring of SystemdListenFDsStart below.
func GetSystemdListeners(start int) []net.Listener {
	defer func() {
		_ = os.Unsetenv("LISTEN_PID")
		_ = os.Unsetenv("LISTEN_FDS")
	}()

	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil {
		return nil
	}

	if pid != os.Getpid() {
		return nil
	}

	fds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil {
		return nil
	}

	listeners := []net.Listener{}

	for i := start; i < start+fds; i++ {
		unix.CloseOnExec(i)

		file := os.NewFile(uintptr(i), fmt.Sprintf("inherited-fd%d", i))
		listener, err := net.FileListener(file)
		if err != nil {
			continue
		}

		listeners = append(listeners, listener)
	}

	return listeners
}

// SystemdListenFDsStart is the number of the first file descriptor that might
// have been opened by systemd when socket activation is enabled. It's always 3
// in real-world usage (i.e. the first file descriptor opened after stdin,
// stdout and stderr), so this constant should always be the value passed to
// GetListeners, except for unit tests.
const SystemdListenFDsStart = 3
