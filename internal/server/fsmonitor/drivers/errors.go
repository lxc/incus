package drivers

import (
	"fmt"
)

// ErrInvalidFunction is the "Invalid function" error.
var ErrInvalidFunction = fmt.Errorf("Invalid function")

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

// ErrWatchExists is the "Watch already exists" error.
var ErrWatchExists = fmt.Errorf("Watch already exists")

// ErrInvalidPath is the "Invalid path" error.
type ErrInvalidPath struct {
	PrefixPath string
}

// Error returns the error string.
func (e *ErrInvalidPath) Error() string {
	return fmt.Sprintf("Path needs to be in %s", e.PrefixPath)
}
