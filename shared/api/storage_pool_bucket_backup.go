package api

import (
	"time"
)

// StorageBucketBackup represents the fields available for a new storage bucket backup
//
// swagger:model
//
// API extension: storage_bucket_backup.
type StorageBucketBackup struct {
	// Backup name
	// Example: backup0
	Name string `json:"name" yaml:"name"`

	// When the backup expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// What compression algorithm to use
	// Example: gzip
	CompressionAlgorithm string `json:"compression_algorithm" yaml:"compression_algorithm"`
}

// StorageBucketBackupsPost represents the fields available for a new storage bucket backup
//
// swagger:model
//
// API extension: storage_bucket_backup.
type StorageBucketBackupsPost struct {
	// Backup name
	// Example: backup0
	Name string `json:"name" yaml:"name"`

	// When the backup expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// What compression algorithm to use
	// Example: gzip
	CompressionAlgorithm string `json:"compression_algorithm" yaml:"compression_algorithm"`
}

// StorageBucketBackupPost represents the fields available for the renaming of a bucket backup
//
// swagger:model
//
// API extension: storage_bucket_backup.
type StorageBucketBackupPost struct {
	// New backup name
	// Example: backup1
	Name string `json:"name" yaml:"name"`
}
