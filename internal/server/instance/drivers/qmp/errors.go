package qmp

import (
	"errors"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = errors.New("Monitor is disconnected")

// ErrMonitorTimeout is returned when a command doesn't get a reply in time (e.g. unresponsive QEMU).
var ErrMonitorTimeout = errors.New("Monitor command timed out")

// ErrMonitorBadConsole is returned when the requested console doesn't exist.
var ErrMonitorBadConsole = errors.New("Requested console couldn't be found")

// ErrNotARingbuf is returned when the requested device isn't a ring buffer.
var ErrNotARingbuf = errors.New("Requested device isn't a ring buffer")
