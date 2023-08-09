package qmp

import (
	"fmt"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = fmt.Errorf("Monitor is disconnected")

// ErrMonitorBadConsole is retuned when the requested console doesn't exist.
var ErrMonitorBadConsole = fmt.Errorf("Requested console couldn't be found")
