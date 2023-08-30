package api

import (
	"time"
)

// StoragePoolVolumeBackup represents a volume backup
//
// swagger:model
//
// API extension: custom_volume_backup.
type StoragePoolVolumeBackup struct {
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
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`

	// Whether to use a pool-optimized binary format (instead of plain tarball)
	// Example: true
	OptimizedStorage bool `json:"optimized_storage" yaml:"optimized_storage"`
}

// StoragePoolVolumeBackupsPost represents the fields available for a new volume backup
//
// swagger:model
//
// API extension: custom_volume_backup.
type StoragePoolVolumeBackupsPost struct {
	// Backup name
	// Example: backup0
	Name string `json:"name" yaml:"name"`

	// When the backup expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// Whether to ignore snapshots
	// Example: false
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`

	// Whether to use a pool-optimized binary format (instead of plain tarball)
	// Example: true
	OptimizedStorage bool `json:"optimized_storage" yaml:"optimized_storage"`

	// What compression algorithm to use
	// Example: gzip
	CompressionAlgorithm string `json:"compression_algorithm" yaml:"compression_algorithm"`
}

// StoragePoolVolumeBackupPost represents the fields available for the renaming of a volume backup
//
// swagger:model
//
// API extension: custom_volume_backup.
type StoragePoolVolumeBackupPost struct {
	// New backup name
	// Example: backup1
	Name string `json:"name" yaml:"name"`
}
