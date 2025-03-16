package cliconfig

import (
	"fmt"
)

// ErrNotLinux is returned when attempting to access the "local" remote on non-Linux systems.
var ErrNotLinux = fmt.Errorf("Can't connect to a local server on a non-Linux system")
