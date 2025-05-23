package cliconfig

import (
	"errors"
)

// ErrNotLinux is returned when attempting to access the "local" remote on non-Linux systems.
var ErrNotLinux = errors.New("Can't connect to a local server on a non-Linux system")
