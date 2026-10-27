package api

// StorageVolumeState represents the live state of the volume
//
// swagger:model
//
// API extension: storage_volume_state.
type StorageVolumeState struct {
	// Volume usage
	Usage *StorageVolumeStateUsage `json:"usage" yaml:"usage"`
}

// StorageVolumeStateUsage represents the disk usage of a volume
//
// swagger:model
//
// API extension: storage_volume_state.
type StorageVolumeStateUsage struct {
	// Used space in bytes
	// Example: 1693552640
	Used uint64 `json:"used,omitempty" yaml:"used,omitempty"`

	// Storage volume size in bytes
	// Example: 5189222192
	//
	// API extension: storage_volume_state_total
	Total int64 `json:"total" yaml:"total"`
}
