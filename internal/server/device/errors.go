package device

import (
	"fmt"
)

// UnsupportedError used for indicating the error is caused due to a lack of support.
type UnsupportedError struct {
	msg string
}

func (e UnsupportedError) Error() string {
	return e.msg
}

// ErrUnsupportedDevType is the error that occurs when an unsupported device type is created.
var ErrUnsupportedDevType = UnsupportedError{msg: "Unsupported device type"}

// ErrCannotUpdate is the error that occurs when a device cannot be updated.
var ErrCannotUpdate = fmt.Errorf("Device does not support updates")

// ErrMissingVirtiofsd is the error that occurs if virtiofsd is missing.
var ErrMissingVirtiofsd = UnsupportedError{msg: "Virtiofsd missing"}
