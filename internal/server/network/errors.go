package network

import (
	"fmt"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

// ErrNotImplemented is the "Not implemented" error.
var ErrNotImplemented = fmt.Errorf("Not implemented")
