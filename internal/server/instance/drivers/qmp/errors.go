package qmp

import (
	"errors"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = errors.New("Monitor is disconnected")

// ErrMonitorBadConsole is returned when the requested console doesn't exist.
var ErrMonitorBadConsole = errors.New("Requested console couldn't be found")

// ErrNotARingbuf is returned when the requested device isn't a ring buffer.
var ErrNotARingbuf = errors.New("Requested device isn't a ring buffer")
