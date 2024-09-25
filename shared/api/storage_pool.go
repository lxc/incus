package api

// StoragePoolStatusPending storage pool is pending creation on other cluster nodes.
const StoragePoolStatusPending = "Pending"

// StoragePoolStatusCreated storage pool is fully created.
const StoragePoolStatusCreated = "Created"

// StoragePoolStatusErrored storage pool is in error status.
const StoragePoolStatusErrored = "Errored"

// StoragePoolStatusUnknown storage pool is in unknown status.
const StoragePoolStatusUnknown = "Unknown"

// StoragePoolStatusUnvailable storage pool failed to initialize.
const StoragePoolStatusUnvailable = "Unavailable"

// StoragePoolsPost represents the fields of a new storage pool
//
// swagger:model
//
// API extension: storage.
type StoragePoolsPost struct {
	StoragePoolPut `yaml:",inline"`

	// Storage pool name
	// Example: local
	Name string `json:"name" yaml:"name"`

	// Storage pool driver (btrfs, ceph, cephfs, cephobject, dir, lvm, lvmcluster or zfs)
	// Example: zfs
	Driver string `json:"driver" yaml:"driver"`
}

// StoragePool represents the fields of a storage pool.
//
// swagger:model
//
// API extension: storage.
type StoragePool struct {
	StoragePoolPut `yaml:",inline"`

	// Storage pool name
	// Example: local
	Name string `json:"name" yaml:"name"`

	// Storage pool driver (btrfs, ceph, cephfs, cephobject, dir, lvm, lvmcluster or zfs)
	// Example: zfs
	Driver string `json:"driver" yaml:"driver"`

	// List of URLs of objects using this storage pool
	// Example: ["/1.0/profiles/default", "/1.0/instances/c1"]
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// Pool status (Pending, Created, Errored or Unknown)
	// Read only: true
	// Example: Created
	//
	// API extension: clustering
	Status string `json:"status" yaml:"status"`

	// Cluster members on which the storage pool has been defined
	// Read only: true
	// Example: ["server01", "server02", "server03"]
	//
	// API extension: clustering
	Locations []string `json:"locations" yaml:"locations"`
}

// StoragePoolPut represents the modifiable fields of a storage pool.
//
// swagger:model
//
// API extension: storage.
type StoragePoolPut struct {
	// Storage pool configuration map (refer to doc/storage.md)
	// Example: {"volume.block.filesystem": "ext4", "volume.size": "50GiB"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the storage pool
	// Example: Local SSD pool
	//
	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// Writable converts a full StoragePool struct into a StoragePoolPut struct
// (filters read-only fields).
func (storagePool *StoragePool) Writable() StoragePoolPut {
	return storagePool.StoragePoolPut
}

// StoragePoolState represents the state of a storage pool.
//
// swagger:model
//
// API extension: cluster_member_state.
type StoragePoolState struct {
	ResourcesStoragePool `yaml:",inline"`
}
