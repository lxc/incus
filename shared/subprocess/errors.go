//go:build !windows

package subprocess

import (
	"errors"
)

// ErrNotRunning is returned when performing an action against a stopped process.
var ErrNotRunning = errors.New("The process isn't running")
