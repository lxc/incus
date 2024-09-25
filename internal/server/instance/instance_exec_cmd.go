package instance

import (
	"golang.org/x/sys/unix"
)

// Cmd represents a local or remote command being run.
type Cmd interface {
	Wait() (int, error)
	PID() int
	Signal(s unix.Signal) error
	WindowResize(fd, winchWidth, winchHeight int) error
}
