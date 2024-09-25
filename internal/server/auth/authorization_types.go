package auth

// Entitlement is a type representation of a permission as it applies to a particular ObjectType.
type Entitlement string

const (
	// Entitlements that apply to all resources.
	EntitlementCanEdit Entitlement = "can_edit"
	EntitlementCanView Entitlement = "can_view"

	// Server entitlements.
	EntitlementCanCreateCertificates               Entitlement = "can_create_certificates"
	EntitlementCanCreateNetworkIntegrations        Entitlement = "can_create_network_integrations"
	EntitlementCanCreateProjects                   Entitlement = "can_create_projects"
	EntitlementCanCreateStoragePools               Entitlement = "can_create_storage_pools"
	EntitlementCanOverrideClusterTargetRestriction Entitlement = "can_override_cluster_target_restriction"
	EntitlementCanViewMetrics                      Entitlement = "can_view_metrics"
	EntitlementCanViewPrivilegedEvents             Entitlement = "can_view_privileged_events"
	EntitlementCanViewResources                    Entitlement = "can_view_resources"

	// Project entitlements.
	EntitlementCanCreateImageAliases   Entitlement = "can_create_image_aliases"
	EntitlementCanCreateImages         Entitlement = "can_create_images"
	EntitlementCanCreateInstances      Entitlement = "can_create_instances"
	EntitlementCanCreateNetworkACLs    Entitlement = "can_create_network_acls"
	EntitlementCanCreateNetworks       Entitlement = "can_create_networks"
	EntitlementCanCreateNetworkZones   Entitlement = "can_create_network_zones"
	EntitlementCanCreateProfiles       Entitlement = "can_create_profiles"
	EntitlementCanCreateStorageBuckets Entitlement = "can_create_storage_buckets"
	EntitlementCanCreateStorageVolumes Entitlement = "can_create_storage_volumes"
	EntitlementCanViewEvents           Entitlement = "can_view_events"
	EntitlementCanViewOperations       Entitlement = "can_view_operations"

	// Instance entitlements.
	EntitlementCanAccessConsole Entitlement = "can_access_console"
	EntitlementCanAccessFiles   Entitlement = "can_access_files"
	EntitlementCanConnectSFTP   Entitlement = "can_connect_sftp"
	EntitlementCanExec          Entitlement = "can_exec"
	EntitlementCanUpdateState   Entitlement = "can_update_state"

	// Instance and storage volume entitlements.
	EntitlementCanManageBackups   Entitlement = "can_manage_backups"
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
