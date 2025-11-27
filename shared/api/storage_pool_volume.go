package api

import (
	"time"
)

// StorageVolumesPost represents the fields of a new storage pool volume
//
// swagger:model
//
// API extension: storage.
type StorageVolumesPost struct {
	StorageVolumePut `yaml:",inline"`

	// Volume name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Volume type (container, custom, image or virtual-machine)
	// Example: custom
	Type string `json:"type" yaml:"type"`

	// Migration source
	//
	// API extension: storage_api_local_volume_handling
	Source StorageVolumeSource `json:"source" yaml:"source"`

	// Volume content type (filesystem or block)
	// Example: filesystem
	//
	// API extension: custom_block_volumes
	ContentType string `json:"content_type" yaml:"content_type"`
}

// StorageVolumePost represents the fields required to rename a storage pool volume
//
// swagger:model
//
// API extension: storage_api_volume_rename.
type StorageVolumePost struct {
	// New volume name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// New storage pool
	// Example: remote
	//
	// API extension: storage_api_local_volume_handling
	Pool string `json:"pool,omitempty" yaml:"pool,omitempty"`

	// Initiate volume migration
	// Example: false
	//
	// API extension: storage_api_remote_volume_handling
	Migration bool `json:"migration" yaml:"migration"`

	// Migration target (for push mode)
	//
	// API extension: storage_api_remote_volume_handling
	Target *StorageVolumePostTarget `json:"target" yaml:"target"`

	// Whether snapshots should be discarded (migration only)
	// Example: false
	//
	// API extension: storage_api_remote_volume_snapshots
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`

	// New project name
	// Example: foo
	//
	// API extension: storage_volume_project_move
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// Migration source
	//
	// API extension: cluster_internal_custom_volume_copy
	Source StorageVolumeSource `json:"source" yaml:"source"`
}

// StorageVolumePostTarget represents the migration target host and operation
//
// swagger:model
//
// API extension: storage_api_remote_volume_handling.
type StorageVolumePostTarget struct {
	// The certificate of the migration target
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Remote operation URL (for migration)
	// Example: https://1.2.3.4:8443/1.0/operations/1721ae08-b6a8-416a-9614-3f89302466e1
	Operation string `json:"operation,omitempty" yaml:"operation,omitempty"`

	// Migration websockets credentials
	// Example: {"migration": "random-string"}
	Websockets map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// StorageVolumeFull is a combination of StorageVolume, StorageVolumeBackup, StorageVolumeSnapshot and StorageVolumeState.
//
// swagger:model
//
// API extension: storage_volume_full.
type StorageVolumeFull struct {
	StorageVolume `yaml:",inline"`

	// List of backups.
	Backups []StorageVolumeBackup `json:"backups" yaml:"backups"`

	// List of snapshots.
	Snapshots []StorageVolumeSnapshot `json:"snapshots" yaml:"snapshots"`

	// State.
	State *StorageVolumeState `json:"state" yaml:"state"`
}

// StorageVolume represents the fields of a storage volume.
//
// swagger:model
//
// API extension: storage.
type StorageVolume struct {
	StorageVolumePut `yaml:",inline"`

	// Volume name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Volume type
	// Example: custom
	Type string `json:"type" yaml:"type"`

	// List of URLs of objects using this storage volume
	// Example: ["/1.0/instances/blah"]
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// What cluster member this record was found on
	// Example: server01
	//
	// API extension: clustering
	Location string `json:"location" yaml:"location"`

	// Volume content type (filesystem or block)
	// Example: filesystem
	//
	// API extension: custom_block_volumes
	ContentType string `json:"content_type" yaml:"content_type"`

	// Project containing the volume.
	// Example: default
	//
	// API extension: storage_volumes_all_projects
	Project string `json:"project" yaml:"project"`

	// Volume creation timestamp
	// Example: 2021-03-23T20:00:00-04:00
	// API extension: storage_volumes_created_at
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
}

// URL returns the URL for the volume.
func (v *StorageVolume) URL(apiVersion string, poolName string) *URL {
	u := NewURL()

	volName, snapName, isSnap := GetParentAndSnapshotName(v.Name)
	if isSnap {
		u = u.Path(apiVersion, "storage-pools", poolName, "volumes", v.Type, volName, "snapshots", snapName)
	} else {
		u = u.Path(apiVersion, "storage-pools", poolName, "volumes", v.Type, volName)
	}

	return u.Project(v.Project).Target(v.Location)
}

// StorageVolumePut represents the modifiable fields of a storage volume
//
// swagger:model
//
// API extension: storage.
type StorageVolumePut struct {
	// Storage volume configuration map (refer to doc/storage.md)
	// Example: {"zfs.remove_snapshots": "true", "size": "50GiB"}
	Config ConfigMap `json:"config" yaml:"config"`

	// Description of the storage volume
	// Example: My custom volume
	//
	// API extension: entity_description
	Description string `json:"description" yaml:"description"`

	// Name of a snapshot to restore
	// Example: snap0
	//
	// API extension: storage_api_volume_snapshots
	Restore string `json:"restore,omitempty" yaml:"restore,omitempty"`
}

// StorageVolumeSource represents the creation source for a new storage volume
//
// swagger:model
//
// API extension: storage_api_local_volume_handling.
type StorageVolumeSource struct {
	// Source volume name (for copy)
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Source type (copy or migration)
	// Example: copy
	Type string `json:"type" yaml:"type"`

	// Source storage pool (for copy)
	// Example: local
	Pool string `json:"pool" yaml:"pool"`

	// Certificate (for migration)
	// Example: X509 PEM certificate
	//
	// API extension: storage_api_remote_volume_handling
	Certificate string `json:"certificate" yaml:"certificate"`

	// Whether to use pull or push mode (for migration)
	// Example: pull
	//
	// API extension: storage_api_remote_volume_handling
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`

	// Remote operation URL (for migration)
	// Example: https://1.2.3.4:8443/1.0/operations/1721ae08-b6a8-416a-9614-3f89302466e1
	//
	// API extension: storage_api_remote_volume_handling
	Operation string `json:"operation,omitempty" yaml:"operation,omitempty"`

	// Map of migration websockets (for migration)
	// Example: {"rsync": "RANDOM-STRING"}
	//
	// API extension: storage_api_remote_volume_handling
	Websockets map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`

	// Whether snapshots should be discarded (for migration)
	// Example: false
	//
	// API extension: storage_api_volume_snapshots
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`

	// Whether existing destination volume should be refreshed
	// Example: false
	//
	// API extension: custom_volume_refresh
	Refresh bool `json:"refresh" yaml:"refresh"`

	// Whether to exclude source snapshots earlier than latest target snapshot
	// Example: false
	//
	// API extension: custom_volume_refresh_exclude_older_snapshots
	RefreshExcludeOlder bool `json:"refresh_exclude_older" yaml:"refresh_exclude_older"`

	// Source project name
	// Example: foo
	//
	// API extension: storage_api_project
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// What cluster member this record was found on
	// Example: server01
	//
	// API extension: cluster_internal_custom_volume_copy
	Location string `json:"location" yaml:"location"`
}

// Writable converts a full StorageVolume struct into a StorageVolumePut struct (filters read-only fields).
func (v *StorageVolume) Writable() StorageVolumePut {
	return v.StorageVolumePut
}
