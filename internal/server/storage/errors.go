package storage

import (
	"errors"
)

// ErrNilValue is the "Nil value provided" error.
var ErrNilValue = errors.New("Nil value provided")

// ErrBackupSnapshotsMismatch is the "Backup snapshots mismatch" error.
var ErrBackupSnapshotsMismatch = errors.New("Backup snapshots mismatch")
