package network

import (
	"errors"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = errors.New("Unknown driver")

// ErrNotImplemented is the "Not implemented" error.
var ErrNotImplemented = errors.New("Not implemented")
