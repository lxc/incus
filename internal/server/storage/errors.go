package storage

import (
	"errors"
)

// ErrNilValue is the "Nil value provided" error.
var ErrNilValue = errors.New("Nil value provided")

// ErrBackupSnapshotsMismatch is the "Backup snapshots mismatch" error.
var ErrBackupSnapshotsMismatch = errors.New("Backup snapshots mismatch")

// ErrVolumeNotAttachedToRunningInstance is the "Volume is not attached to running instance" error.
var ErrVolumeNotAttachedToRunningInstance = errors.New("Volume is not attached to running instance")
