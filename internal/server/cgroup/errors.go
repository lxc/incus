package cgroup

import (
	"errors"
)

// ErrControllerMissing indicates that the requested controller isn't setup on the system.
var ErrControllerMissing = errors.New("Cgroup controller is missing")

// ErrUnknownVersion indicates that a version other than those supported was detected during init.
var ErrUnknownVersion = errors.New("Unknown cgroup version")
