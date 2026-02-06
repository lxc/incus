package backup

import (
	"time"

	"github.com/lxc/incus/v6/internal/server/state"
)

// WorkingDirPrefix is used when temporary working directories are needed.
const WorkingDirPrefix = "incus_backup"

// CommonBackup represents a common backup.
type CommonBackup struct {
	state                *state.State
	id                   int
	name                 string
	creationDate         time.Time
	expiryDate           time.Time
	optimizedStorage     bool
	compressionAlgorithm string
}

// Name returns the name of the backup.
func (b *CommonBackup) Name() string {
	return b.name
}

// CompressionAlgorithm returns the compression used for the tarball.
func (b *CommonBackup) CompressionAlgorithm() string {
	return b.compressionAlgorithm
}

// SetCompressionAlgorithm sets the tarball compression.
func (b *CommonBackup) SetCompressionAlgorithm(compression string) {
	b.compressionAlgorithm = compression
}

// OptimizedStorage returns whether the backup is to be performed using
// optimization supported by the storage driver.
func (b *CommonBackup) OptimizedStorage() bool {
	return b.optimizedStorage
}
