package cgroup

import (
	"fmt"
)

// ErrControllerMissing indicates that the requested controller isn't setup on the system.
var ErrControllerMissing = fmt.Errorf("Cgroup controller is missing")

// ErrUnknownVersion indicates that a version other than those supported was detected during init.
var ErrUnknownVersion = fmt.Errorf("Unknown cgroup version")
