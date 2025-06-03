package api

import (
	"time"
)

// BackupTarget represents the target storage server for an instance or volume backup.
//
// swagger:model
//
// API extension: backup_s3_upload.
type BackupTarget struct {
	// Protocol is the upload protocol.
	// Example: S3
	Protocol string `json:"protocol" yaml:"protocol"`

	// URL is the HTTPS URL for the backup
	// Example: https://storage.googleapis.com
	URL string `json:"url" yaml:"url"`

	// BucketName is the name of the S3 bucket.
	// Example: my_bucket
	BucketName string `json:"bucket_name" yaml:"bucket_name"`

	// Path is the target path.
	// Example: foo/test.tar
	Path string `json:"path" yaml:"path"`

	// AccessKey is the S3 API access key
	// Example: GOOG1234
	AccessKey string `json:"access_key" yaml:"access_key"`

	// SecretKey is the S3 API access key
	// Example: secret123
	SecretKey string `json:"secret_key" yaml:"secret_key"`
}

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

	// External upload target
	// The backup will be uploaded and then deleted from local storage.
	//
	// API extension: backup_s3_upload
	Target *BackupTarget `json:"target" yaml:"target"`
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
