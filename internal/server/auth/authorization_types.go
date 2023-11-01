package auth

// Entitlement is a type representation of a permission as it applies to a particular ObjectType.
type Entitlement string

const (
	// Entitlements that apply to all resources.
	EntitlementCanEdit Entitlement = "can_edit"
	EntitlementCanView Entitlement = "can_view"

	// Server entitlements.
	EntitlementCanCreateStoragePools               Entitlement = "can_create_storage_pools"
	EntitlementCanCreateProjects                   Entitlement = "can_create_projects"
	EntitlementCanViewResources                    Entitlement = "can_view_resources"
	EntitlementCanCreateCertificates               Entitlement = "can_create_certificates"
	EntitlementCanViewMetrics                      Entitlement = "can_view_metrics"
	EntitlementCanOverrideClusterTargetRestriction Entitlement = "can_override_cluster_target_restriction"
	EntitlementCanViewPrivilegedEvents             Entitlement = "can_view_privileged_events"

	// Project entitlements.
	EntitlementCanCreateImages         Entitlement = "can_create_images"
	EntitlementCanCreateImageAliases   Entitlement = "can_create_image_aliases"
	EntitlementCanCreateInstances      Entitlement = "can_create_instances"
	EntitlementCanCreateNetworks       Entitlement = "can_create_networks"
	EntitlementCanCreateNetworkACLs    Entitlement = "can_create_network_acls"
	EntitlementCanCreateNetworkZones   Entitlement = "can_create_network_zones"
	EntitlementCanCreateProfiles       Entitlement = "can_create_profiles"
	EntitlementCanCreateStorageVolumes Entitlement = "can_create_storage_volumes"
	EntitlementCanCreateStorageBuckets Entitlement = "can_create_storage_buckets"
	EntitlementCanViewOperations       Entitlement = "can_view_operations"
	EntitlementCanViewEvents           Entitlement = "can_view_events"

	// Instance entitlements.
	EntitlementCanUpdateState   Entitlement = "can_update_state"
	EntitlementCanConnectSFTP   Entitlement = "can_connect_sftp"
	EntitlementCanAccessFiles   Entitlement = "can_access_files"
	EntitlementCanAccessConsole Entitlement = "can_access_console"
	EntitlementCanExec          Entitlement = "can_exec"

	// Instance and storage volume entitlements.
	EntitlementCanManageSnapshots Entitlement = "can_manage_snapshots"
	EntitlementCanManageBackups   Entitlement = "can_manage_backups"
)

// ObjectType is a type of resource within Incus.
type ObjectType string

const (
	ObjectTypeUser          ObjectType = "user"
	ObjectTypeServer        ObjectType = "server"
	ObjectTypeCertificate   ObjectType = "certificate"
	ObjectTypeStoragePool   ObjectType = "storage_pool"
	ObjectTypeProject       ObjectType = "project"
	ObjectTypeImage         ObjectType = "image"
	ObjectTypeImageAlias    ObjectType = "image_alias"
	ObjectTypeInstance      ObjectType = "instance"
	ObjectTypeNetwork       ObjectType = "network"
	ObjectTypeNetworkACL    ObjectType = "network_acl"
	ObjectTypeNetworkZone   ObjectType = "network_zone"
	ObjectTypeProfile       ObjectType = "profile"
	ObjectTypeStorageBucket ObjectType = "storage_bucket"
	ObjectTypeStorageVolume ObjectType = "storage_volume"
)

// Permission is a type representation of general permission levels in Incus. Used with TLS and RBAC drivers.
type Permission string

const (
	PermissionAdmin                Permission = "admin"
	PermissionView                 Permission = "view"
	PermissionManageProjects       Permission = "manage-projects"
	PermissionManageInstances      Permission = "manage-containers"
	PermissionManageImages         Permission = "manage-images"
	PermissionManageNetworks       Permission = "manage-networks"
	PermissionManageProfiles       Permission = "manage-profiles"
	PermissionManageStorageVolumes Permission = "manage-storage-volumes"
	PermissionOperateInstances     Permission = "operate-containers"
)

const (
	relationServer  = "server"
	relationProject = "project"
	relationUser    = "user"
)
