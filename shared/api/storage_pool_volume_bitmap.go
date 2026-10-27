package api

// StorageVolumeBitmap represents a volume bitmap
//
// swagger:model
//
// API extension: storage_volume_nbd.
type StorageVolumeBitmap struct {
	// Bitmap name
	// Example: bitmap0
	Name string `json:"name" yaml:"name"`

	// Number of dirty bytes
	// Example: 300
	Count int `json:"count" yaml:"count"`

	// Granularity of the dirty bitmap in bytes
	// Example: 32768
	Granularity int `json:"granularity" yaml:"granularity"`

	// true if the bitmap is recording new writes from the guest
	// Example: false
	Recording bool `json:"recording" yaml:"recording"`

	// true if the bitmap is in-use by some operation
	// Example: true
	Busy bool `json:"busy" yaml:"busy"`

	// true if the bitmap was stored on disk, is scheduled to be stored on disk, or both
	// Example: false
	Persistent bool `json:"persistent" yaml:"persistent"`

	// true if this is a persistent bitmap that was improperly stored
	// Example: true
	Inconsistent bool `json:"inconsistent" yaml:"inconsistent"`
}

// StorageVolumeBitmapsPost represents the fields available for a new volume bitmap
//
// swagger:model
//
// API extension: satorage_volume_nbd.
type StorageVolumeBitmapsPost struct {
	// Bitmap name
	// Example: bitmap0
	Name string `json:"name" yaml:"name"`

	// Granularity of the dirty bitmap in bytes
	// Example: 32768
	Granularity int `json:"granularity" yaml:"granularity"`

	// true if the bitmap was stored on disk, is scheduled to be stored on disk, or both
	// Example: false
	Persistent bool `json:"persistent" yaml:"persistent"`

	// The bitmap is created in the disabled state
	// Example: false
	Disabled bool `json:"disabled" yaml:"disabled"`
}
