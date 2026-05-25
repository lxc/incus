package auth

// Entitlement is a type representation of a permission as it applies to a particular ObjectType.
type Entitlement string

// Entitlements that apply to all resources.
const (
	// EntitlementCanEdit is the entitlement to edit a resource.
	EntitlementCanEdit Entitlement = "can_edit"

	// EntitlementCanView is the entitlement to view a resource.
	EntitlementCanView Entitlement = "can_view"
)

// Server entitlements.
const (
	// EntitlementCanCreateCertificates is the entitlement to create certificates.
	EntitlementCanCreateCertificates Entitlement = "can_create_certificates"

	// EntitlementCanCreateNetworkIntegrations is the entitlement to create network integrations.
	EntitlementCanCreateNetworkIntegrations Entitlement = "can_create_network_integrations"

	// EntitlementCanCreateProjects is the entitlement to create projects.
	EntitlementCanCreateProjects Entitlement = "can_create_projects"

	// EntitlementCanCreateStoragePools is the entitlement to create storage pools.
	EntitlementCanCreateStoragePools Entitlement = "can_create_storage_pools"

	// EntitlementCanOverrideClusterTargetRestriction is the entitlement to override the cluster target restriction.
	EntitlementCanOverrideClusterTargetRestriction Entitlement = "can_override_cluster_target_restriction"

	// EntitlementCanViewMetrics is the entitlement to view metrics.
	EntitlementCanViewMetrics Entitlement = "can_view_metrics"

	// EntitlementCanViewPrivilegedEvents is the entitlement to view privileged events.
	EntitlementCanViewPrivilegedEvents Entitlement = "can_view_privileged_events"

	// EntitlementCanViewResources is the entitlement to view resources.
	EntitlementCanViewResources Entitlement = "can_view_resources"

	// EntitlementCanViewSensitive is the entitlement to view sensitive information.
	EntitlementCanViewSensitive Entitlement = "can_view_sensitive"
)

// Project entitlements.
const (
	// EntitlementCanCreateImageAliases is the entitlement to create image aliases.
	EntitlementCanCreateImageAliases Entitlement = "can_create_image_aliases"

	// EntitlementCanCreateImages is the entitlement to create images.
	EntitlementCanCreateImages Entitlement = "can_create_images"

	// EntitlementCanCreateInstances is the entitlement to create instances.
	EntitlementCanCreateInstances Entitlement = "can_create_instances"

	// EntitlementCanCreateNetworkACLs is the entitlement to create network ACLs.
	EntitlementCanCreateNetworkACLs Entitlement = "can_create_network_acls"

	// EntitlementCanCreateNetworkAddressSets is the entitlement to create network address sets.
	EntitlementCanCreateNetworkAddressSets Entitlement = "can_create_network_address_sets"

	// EntitlementCanCreateNetworks is the entitlement to create networks.
	EntitlementCanCreateNetworks Entitlement = "can_create_networks"

	// EntitlementCanCreateNetworkZones is the entitlement to create network zones.
	EntitlementCanCreateNetworkZones Entitlement = "can_create_network_zones"

	// EntitlementCanCreateProfiles is the entitlement to create profiles.
	EntitlementCanCreateProfiles Entitlement = "can_create_profiles"

	// EntitlementCanCreateStorageBuckets is the entitlement to create storage buckets.
	EntitlementCanCreateStorageBuckets Entitlement = "can_create_storage_buckets"

	// EntitlementCanCreateStorageVolumes is the entitlement to create storage volumes.
	EntitlementCanCreateStorageVolumes Entitlement = "can_create_storage_volumes"

	// EntitlementCanViewEvents is the entitlement to view events.
	EntitlementCanViewEvents Entitlement = "can_view_events"

	// EntitlementCanViewOperations is the entitlement to view operations.
	EntitlementCanViewOperations Entitlement = "can_view_operations"
)

// Instance entitlements.
const (
	// EntitlementCanAccessConsole is the entitlement to access the console.
	EntitlementCanAccessConsole Entitlement = "can_access_console"

	// EntitlementCanExec is the entitlement to execute commands.
	EntitlementCanExec Entitlement = "can_exec"

	// EntitlementCanUpdateState is the entitlement to update the state.
	EntitlementCanUpdateState Entitlement = "can_update_state"
)

// Instance and storage volume entitlements.
const (
	// EntitlementCanAccessFiles is the entitlement to access files.
	EntitlementCanAccessFiles Entitlement = "can_access_files"

	// EntitlementCanConnectNBD is the entitlement to connect over NBD.
	EntitlementCanConnectNBD Entitlement = "can_connect_nbd"

	// EntitlementCanConnectSFTP is the entitlement to connect over SFTP.
	EntitlementCanConnectSFTP Entitlement = "can_connect_sftp"

	// EntitlementCanManageBackups is the entitlement to manage backups.
	EntitlementCanManageBackups Entitlement = "can_manage_backups"

	// EntitlementCanManageSnapshots is the entitlement to manage snapshots.
	EntitlementCanManageSnapshots Entitlement = "can_manage_snapshots"
)

// ObjectType is a type of resource within Incus.
type ObjectType string

const (
	// ObjectTypeUser represents a user.
	ObjectTypeUser ObjectType = "user"

	// ObjectTypeServer represents a server.
	ObjectTypeServer ObjectType = "server"

	// ObjectTypeCertificate represents a certificate.
	ObjectTypeCertificate ObjectType = "certificate"

	// ObjectTypeStoragePool represents a storage pool.
	ObjectTypeStoragePool ObjectType = "storage_pool"

	// ObjectTypeProject represents a project.
	ObjectTypeProject ObjectType = "project"

	// ObjectTypeImage represents an image.
	ObjectTypeImage ObjectType = "image"

	// ObjectTypeImageAlias represents an image alias.
	ObjectTypeImageAlias ObjectType = "image_alias"

	// ObjectTypeInstance represents an instance.
	ObjectTypeInstance ObjectType = "instance"

	// ObjectTypeNetwork represents a network.
	ObjectTypeNetwork ObjectType = "network"

	// ObjectTypeNetworkACL represents a network ACL.
	ObjectTypeNetworkACL ObjectType = "network_acl"

	// ObjectTypeNetworkAddressSet represents a network address set.
	ObjectTypeNetworkAddressSet ObjectType = "network_address_set"

	// ObjectTypeNetworkIntegration represents a network integration.
	ObjectTypeNetworkIntegration ObjectType = "network_integration"

	// ObjectTypeNetworkZone represents a network zone.
	ObjectTypeNetworkZone ObjectType = "network_zone"

	// ObjectTypeProfile represents a profile.
	ObjectTypeProfile ObjectType = "profile"

	// ObjectTypeStorageBucket represents a storage bucket.
	ObjectTypeStorageBucket ObjectType = "storage_bucket"

	// ObjectTypeStorageVolume represents a storage volume.
	ObjectTypeStorageVolume ObjectType = "storage_volume"
)

const (
	relationServer  = "server"
	relationProject = "project"
)
