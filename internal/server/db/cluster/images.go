//go:build linux && cgo && !agent

package cluster

import (
	"database/sql"
	"time"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t images.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e image objects
//go:generate mapper stmt -e image objects-by-ID
//go:generate mapper stmt -e image objects-by-Project
//go:generate mapper stmt -e image objects-by-Project-and-Cached
//go:generate mapper stmt -e image objects-by-Project-and-Public
//go:generate mapper stmt -e image objects-by-Fingerprint
//go:generate mapper stmt -e image objects-by-Cached
//go:generate mapper stmt -e image objects-by-AutoUpdate
//
//go:generate mapper method -i -e image GetMany
//go:generate mapper method -i -e image GetOne

// Image is a value object holding db-related details about an image.
type Image struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Fingerprint  string `db:"primary=yes"`
	Type         int
	Filename     string
	Size         int64
	Public       bool
	Architecture int
	CreationDate sql.NullTime
	ExpiryDate   sql.NullTime
	UploadDate   time.Time
	Cached       bool
	LastUseDate  sql.NullTime
	AutoUpdate   bool
}

// ImageFilter can be used to filter results yielded by GetImages.
type ImageFilter struct {
	ID          *int
	Project     *string
	Fingerprint *string
	Public      *bool
	Cached      *bool
	AutoUpdate  *bool
}
