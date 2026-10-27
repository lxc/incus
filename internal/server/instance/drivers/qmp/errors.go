package qmp

import (
	"errors"
	"fmt"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = errors.New("Monitor is disconnected")

// ErrMonitorTimeout is returned when a command doesn't get a reply in time (e.g. unresponsive QEMU).
var ErrMonitorTimeout = errors.New("Monitor command timed out")

// ErrMonitorBusy is returned when a command couldn't be sent in time because another one is in flight.
var ErrMonitorBusy = fmt.Errorf("%w while monitor busy", ErrMonitorTimeout)

// ErrMonitorBadConsole is returned when the requested console doesn't exist.
var ErrMonitorBadConsole = errors.New("Requested console couldn't be found")

// ErrNotARingbuf is returned when the requested device isn't a ring buffer.
var ErrNotARingbuf = errors.New("Requested device isn't a ring buffer")
