package qmp

import (
	"fmt"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = fmt.Errorf("Monitor is disconnected")

// ErrMonitorBadConsole is returned when the requested console doesn't exist.
var ErrMonitorBadConsole = fmt.Errorf("Requested console couldn't be found")

// ErrNotARingbuf is returned when the requested device isn't a ring buffer.
var ErrNotARingbuf = fmt.Errorf("Requested device isn't a ring buffer")
