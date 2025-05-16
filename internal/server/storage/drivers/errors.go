package drivers

import (
	"errors"
	"fmt"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = errors.New("Unknown driver")

// ErrNotSupported is the "Not supported" error.
var ErrNotSupported = errors.New("Not supported")

// ErrCannotBeShrunk is the "Cannot be shrunk" error.
var ErrCannotBeShrunk = errors.New("Cannot be shrunk")

// ErrInUse indicates operation cannot proceed as resource is in use.
var ErrInUse = errors.New("In use")

// ErrSnapshotDoesNotMatchIncrementalSource in the "Snapshot does not match incremental source" error.
var ErrSnapshotDoesNotMatchIncrementalSource = errors.New("Snapshot does not match incremental source")

// ErrDeleteSnapshots is a special error used to tell the backend to delete more recent snapshots.
type ErrDeleteSnapshots struct {
	Snapshots []string
}

func (e ErrDeleteSnapshots) Error() string {
	return fmt.Sprintf("More recent snapshots must be deleted: %+v", e.Snapshots)
}
