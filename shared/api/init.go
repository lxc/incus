package api

// InitPreseed represents initialization configuration that can be supplied to `init`.
//
// swagger:model
//
// API extension: preseed.
type InitPreseed struct {
	Server  InitLocalPreseed    `yaml:",inline"`
	Cluster *InitClusterPreseed `json:"cluster" yaml:"cluster"`
}

// InitLocalPreseed represents initialization configuration.
//
// swagger:model
//
// API extension: preseed.
type InitLocalPreseed struct {
	ServerPut `yaml:",inline"`

	// Networks by project to add
	// Example: Network on the "default" project
	Networks []InitNetworksProjectPost `json:"networks" yaml:"networks"`

	// Storage Pools to add
	// Example: local dir storage pool
	StoragePools []StoragePoolsPost `json:"storage_pools" yaml:"storage_pools"`

	// Storage Volumes to add
	// Example: local dir storage volume
	//
	// API extension: init_preseed_storage_volumes.
	StorageVolumes []InitStorageVolumesProjectPost `json:"storage_volumes" yaml:"storage_volumes"`

	// Profiles to add
	// Example: "default" profile with a root disk device
	Profiles []InitProfileProjectPost `json:"profiles" yaml:"profiles"`

	// Projects to add
	// Example: "default" project
	Projects []ProjectsPost `json:"projects" yaml:"projects"`

	// Certificates to add
	// Example: PEM encoded certificate
	//
	// API extension: init_preseed_certificates.
	Certificates []CertificatesPost `json:"certificates" yaml:"certificates"`

	// Cluster groups to add
	//
	// API extension: init_preseed_cluster_groups.
	ClusterGroups []ClusterGroupsPost `json:"cluster_groups" yaml:"cluster_groups"`
}

// InitNetworksProjectPost represents the fields of a new network along with its associated project.
//
// swagger:model
//
// API extension: preseed.
type InitNetworksProjectPost struct {
	NetworksPost `yaml:",inline"`

	// Project in which the network will reside
	// Example: "default"
	Project string
}

// InitProfileProjectPost represents the fields of a new profile along with its associated project.
//
// swagger:model
//
// API extension: init_preseed_profile_project.
type InitProfileProjectPost struct {
	ProfilesPost `yaml:",inline"`

	// Project in which the profile will reside
	// Example: "default"
	Project string
}

// InitStorageVolumesProjectPost represents the fields of a new storage volume along with its associated pool.
//
// swagger:model
//
// API extension: init_preseed_storage_volumes.
type InitStorageVolumesProjectPost struct {
	StorageVolumesPost `yaml:",inline"`

	// Storage pool in which the volume will reside
	// Example: "default"
	Pool string

	// Project in which the volume will reside
	// Example: "default"
	Project string
}

// InitClusterPreseed represents initialization configuration for the cluster.
//
// swagger:model
//
// API extension: preseed.
type InitClusterPreseed struct {
	ClusterPut `yaml:",inline"`

	// The path to the cluster certificate
	// Example: /tmp/cluster.crt
	ClusterCertificatePath string `json:"cluster_certificate_path" yaml:"cluster_certificate_path"`
}
