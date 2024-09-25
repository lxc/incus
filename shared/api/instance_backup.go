package api

import (
	"time"
)

// InstanceBackupsPost represents the fields available for a new instance backup.
//
// swagger:model
//
// API extension: instances.
type InstanceBackupsPost struct {
	// Backup name
	// Example: backup0
	Name string `json:"name" yaml:"name"`

	// When the backup expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// Whether to ignore snapshots
	// Example: false
	InstanceOnly bool `json:"instance_only" yaml:"instance_only"`

	// Whether to use a pool-optimized binary format (instead of plain tarball)
	// Example: true
	OptimizedStorage bool `json:"optimized_storage" yaml:"optimized_storage"`

	// What compression algorithm to use
	// Example: gzip
	//
	// API extension: backup_compression_algorithm
	CompressionAlgorithm string `json:"compression_algorithm" yaml:"compression_algorithm"`
}

// InstanceBackup represents an instance backup.
//
// swagger:model
//
// API extension: instances.
type InstanceBackup struct {
	// Backup name
	// Example: backup0
	Name string `json:"name" yaml:"name"`

	// When the backup was created
	// Example: 2021-03-23T16:38:37.753398689-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// When the backup expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// Whether to ignore snapshots
	// Example: false
	InstanceOnly bool `json:"instance_only" yaml:"instance_only"`

	// Whether to use a pool-optimized binary format (instead of plain tarball)
	// Example: true
	OptimizedStorage bool `json:"optimized_storage" yaml:"optimized_storage"`
}

// InstanceBackupPost represents the fields available for the renaming of a instance backup.
//
// swagger:model
//
// API extension: instances.
type InstanceBackupPost struct {
	// New backup name
	// Example: backup1
	Name string `json:"name" yaml:"name"`
}
