//go:build !windows

package subprocess

import (
	"fmt"
)

// ErrNotRunning is returned when performing an action against a stopped process.
var ErrNotRunning = fmt.Errorf("The process isn't running")
