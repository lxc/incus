package incus

import (
	"context"
	"io"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"

	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/cancel"
	"github.com/lxc/incus/shared/ioprogress"
)

// The Operation type represents a currently running operation.
type Operation interface {
	AddHandler(function func(api.Operation)) (target *EventTarget, err error)
	Cancel() (err error)
	Get() (op api.Operation)
	GetWebsocket(secret string) (conn *websocket.Conn, err error)
	RemoveHandler(target *EventTarget) (err error)
	Refresh() (err error)
	Wait() (err error)
	WaitContext(ctx context.Context) error
}

// The RemoteOperation type represents an Operation that may be using multiple servers.
type RemoteOperation interface {
	AddHandler(function func(api.Operation)) (target *EventTarget, err error)
	CancelTarget() (err error)
	GetTarget() (op *api.Operation, err error)
	Wait() (err error)
}

// The Server type represents a generic read-only server.
type Server interface {
	GetConnectionInfo() (info *ConnectionInfo, err error)
	GetHTTPClient() (client *http.Client, err error)
	DoHTTP(req *http.Request) (resp *http.Response, err error)
	Disconnect()
}

// The ImageServer type represents a read-only image server.
type ImageServer interface {
	Server

	// Image handling functions
	GetImages() (images []api.Image, err error)
	GetImagesAllProjects() (images []api.Image, err error)
	GetImageFingerprints() (fingerprints []string, err error)
	GetImagesWithFilter(filters []string) (images []api.Image, err error)

	GetImage(fingerprint string) (image *api.Image, ETag string, err error)
	GetImageFile(fingerprint string, req ImageFileRequest) (resp *ImageFileResponse, err error)
	GetImageSecret(fingerprint string) (secret string, err error)

	GetPrivateImage(fingerprint string, secret string) (image *api.Image, ETag string, err error)
	GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (resp *ImageFileResponse, err error)

	GetImageAliases() (aliases []api.ImageAliasesEntry, err error)
	GetImageAliasNames() (names []string, err error)

	GetImageAlias(name string) (alias *api.ImageAliasesEntry, ETag string, err error)
	GetImageAliasType(imageType string, name string) (alias *api.ImageAliasesEntry, ETag string, err error)
	GetImageAliasArchitectures(imageType string, name string) (entries map[string]*api.ImageAliasesEntry, err error)

	ExportImage(fingerprint string, image api.ImageExportPost) (Operation, error)
}

// The InstanceServer type represents a full featured Incus server.
type InstanceServer interface {
	ImageServer

	// Server functions
	GetMetrics() (metrics string, err error)
	GetServer() (server *api.Server, ETag string, err error)
	GetServerResources() (resources *api.Resources, err error)
	UpdateServer(server api.ServerPut, ETag string) (err error)
	ApplyServerPreseed(config api.InitPreseed) error
	HasExtension(extension string) (exists bool)
	RequireAuthenticated(authenticated bool)
	IsClustered() (clustered bool)
	UseTarget(name string) (client InstanceServer)
	UseProject(name string) (client InstanceServer)

	// Certificate functions
	GetCertificateFingerprints() (fingerprints []string, err error)
	GetCertificates() (certificates []api.Certificate, err error)
	GetCertificate(fingerprint string) (certificate *api.Certificate, ETag string, err error)
	CreateCertificate(certificate api.CertificatesPost) (err error)
	UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) (err error)
	DeleteCertificate(fingerprint string) (err error)
	CreateCertificateToken(certificate api.CertificatesPost) (op Operation, err error)

	// Instance functions.
	GetInstanceNames(instanceType api.InstanceType) (names []string, err error)
	GetInstanceNamesAllProjects(instanceType api.InstanceType) (names map[string][]string, err error)
	GetInstances(instanceType api.InstanceType) (instances []api.Instance, err error)
	GetInstancesFull(instanceType api.InstanceType) (instances []api.InstanceFull, err error)
	GetInstancesAllProjects(instanceType api.InstanceType) (instances []api.Instance, err error)
	GetInstancesFullAllProjects(instanceType api.InstanceType) (instances []api.InstanceFull, err error)
	GetInstancesWithFilter(instanceType api.InstanceType, filters []string) (instances []api.Instance, err error)
	GetInstancesFullWithFilter(instanceType api.InstanceType, filters []string) (instances []api.InstanceFull, err error)
	GetInstancesAllProjectsWithFilter(instanceType api.InstanceType, filters []string) (instances []api.Instance, err error)
	GetInstancesFullAllProjectsWithFilter(instanceType api.InstanceType, filters []string) (instances []api.InstanceFull, err error)
	GetInstance(name string) (instance *api.Instance, ETag string, err error)
	GetInstanceFull(name string) (instance *api.InstanceFull, ETag string, err error)
	CreateInstance(instance api.InstancesPost) (op Operation, err error)
	CreateInstanceFromImage(source ImageServer, image api.Image, req api.InstancesPost) (op RemoteOperation, err error)
	CopyInstance(source InstanceServer, instance api.Instance, args *InstanceCopyArgs) (op RemoteOperation, err error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (op Operation, err error)
	RenameInstance(name string, instance api.InstancePost) (op Operation, err error)
	MigrateInstance(name string, instance api.InstancePost) (op Operation, err error)
	DeleteInstance(name string) (op Operation, err error)
	UpdateInstances(state api.InstancesPut, ETag string) (op Operation, err error)
	RebuildInstance(instanceName string, req api.InstanceRebuildPost) (op Operation, err error)
	RebuildInstanceFromImage(source ImageServer, image api.Image, instanceName string, req api.InstanceRebuildPost) (op RemoteOperation, err error)

	ExecInstance(instanceName string, exec api.InstanceExecPost, args *InstanceExecArgs) (op Operation, err error)
	ConsoleInstance(instanceName string, console api.InstanceConsolePost, args *InstanceConsoleArgs) (op Operation, err error)
	ConsoleInstanceDynamic(instanceName string, console api.InstanceConsolePost, args *InstanceConsoleArgs) (Operation, func(io.ReadWriteCloser) error, error)

	GetInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) (content io.ReadCloser, err error)
	DeleteInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) (err error)

	GetInstanceFile(instanceName string, path string) (content io.ReadCloser, resp *InstanceFileResponse, err error)
	CreateInstanceFile(instanceName string, path string, args InstanceFileArgs) (err error)
	DeleteInstanceFile(instanceName string, path string) (err error)

	GetInstanceFileSFTPConn(instanceName string) (net.Conn, error)
	GetInstanceFileSFTP(instanceName string) (*sftp.Client, error)

	GetInstanceSnapshotNames(instanceName string) (names []string, err error)
	GetInstanceSnapshots(instanceName string) (snapshots []api.InstanceSnapshot, err error)
	GetInstanceSnapshot(instanceName string, name string) (snapshot *api.InstanceSnapshot, ETag string, err error)
	CreateInstanceSnapshot(instanceName string, snapshot api.InstanceSnapshotsPost) (op Operation, err error)
	CopyInstanceSnapshot(source InstanceServer, instanceName string, snapshot api.InstanceSnapshot, args *InstanceSnapshotCopyArgs) (op RemoteOperation, err error)
	RenameInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (op Operation, err error)
	MigrateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (op Operation, err error)
	DeleteInstanceSnapshot(instanceName string, name string) (op Operation, err error)
	UpdateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPut, ETag string) (op Operation, err error)

	GetInstanceBackupNames(instanceName string) (names []string, err error)
	GetInstanceBackups(instanceName string) (backups []api.InstanceBackup, err error)
	GetInstanceBackup(instanceName string, name string) (backup *api.InstanceBackup, ETag string, err error)
	CreateInstanceBackup(instanceName string, backup api.InstanceBackupsPost) (op Operation, err error)
	RenameInstanceBackup(instanceName string, name string, backup api.InstanceBackupPost) (op Operation, err error)
	DeleteInstanceBackup(instanceName string, name string) (op Operation, err error)
	GetInstanceBackupFile(instanceName string, name string, req *BackupFileRequest) (resp *BackupFileResponse, err error)
	CreateInstanceFromBackup(args InstanceBackupArgs) (op Operation, err error)

	GetInstanceState(name string) (state *api.InstanceState, ETag string, err error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (op Operation, err error)

	GetInstanceLogfiles(name string) (logfiles []string, err error)
	GetInstanceLogfile(name string, filename string) (content io.ReadCloser, err error)
	DeleteInstanceLogfile(name string, filename string) (err error)

	GetInstanceMetadata(name string) (metadata *api.ImageMetadata, ETag string, err error)
	UpdateInstanceMetadata(name string, metadata api.ImageMetadata, ETag string) (err error)

	GetInstanceTemplateFiles(instanceName string) (templates []string, err error)
	GetInstanceTemplateFile(instanceName string, templateName string) (content io.ReadCloser, err error)
	CreateInstanceTemplateFile(instanceName string, templateName string, content io.ReadSeeker) (err error)
	DeleteInstanceTemplateFile(name string, templateName string) (err error)

	// Event handling functions
	GetEvents() (listener *EventListener, err error)
	GetEventsAllProjects() (listener *EventListener, err error)
	SendEvent(event api.Event) error

	// Image functions
	CreateImage(image api.ImagesPost, args *ImageCreateArgs) (op Operation, err error)
	CopyImage(source ImageServer, image api.Image, args *ImageCopyArgs) (op RemoteOperation, err error)
	UpdateImage(fingerprint string, image api.ImagePut, ETag string) (err error)
	DeleteImage(fingerprint string) (op Operation, err error)
	RefreshImage(fingerprint string) (op Operation, err error)
	CreateImageSecret(fingerprint string) (op Operation, err error)
	CreateImageAlias(alias api.ImageAliasesPost) (err error)
	UpdateImageAlias(name string, alias api.ImageAliasesEntryPut, ETag string) (err error)
	RenameImageAlias(name string, alias api.ImageAliasesEntryPost) (err error)
	DeleteImageAlias(name string) (err error)

	// Configuration metadata functions
	GetMetadataConfiguration() (meta *api.MetadataConfiguration, err error)

	// Network functions ("network" API extension)
	GetNetworkNames() (names []string, err error)
	GetNetworks() (networks []api.Network, err error)
	GetNetwork(name string) (network *api.Network, ETag string, err error)
	GetNetworkLeases(name string) (leases []api.NetworkLease, err error)
	GetNetworkState(name string) (state *api.NetworkState, err error)
	CreateNetwork(network api.NetworksPost) (err error)
	UpdateNetwork(name string, network api.NetworkPut, ETag string) (err error)
	RenameNetwork(name string, network api.NetworkPost) (err error)
	DeleteNetwork(name string) (err error)

	// Network forward functions ("network_forward" API extension)
	GetNetworkForwardAddresses(networkName string) ([]string, error)
	GetNetworkForwards(networkName string) ([]api.NetworkForward, error)
	GetNetworkForward(networkName string, listenAddress string) (forward *api.NetworkForward, ETag string, err error)
	CreateNetworkForward(networkName string, forward api.NetworkForwardsPost) error
	UpdateNetworkForward(networkName string, listenAddress string, forward api.NetworkForwardPut, ETag string) (err error)
	DeleteNetworkForward(networkName string, listenAddress string) (err error)

	// Network load balancer functions ("network_load_balancer" API extension)
	GetNetworkLoadBalancerAddresses(networkName string) ([]string, error)
	GetNetworkLoadBalancers(networkName string) ([]api.NetworkLoadBalancer, error)
	GetNetworkLoadBalancer(networkName string, listenAddress string) (forward *api.NetworkLoadBalancer, ETag string, err error)
	CreateNetworkLoadBalancer(networkName string, forward api.NetworkLoadBalancersPost) error
	UpdateNetworkLoadBalancer(networkName string, listenAddress string, forward api.NetworkLoadBalancerPut, ETag string) (err error)
	DeleteNetworkLoadBalancer(networkName string, listenAddress string) (err error)

	// Network peer functions ("network_peer" API extension)
	GetNetworkPeerNames(networkName string) ([]string, error)
	GetNetworkPeers(networkName string) ([]api.NetworkPeer, error)
	GetNetworkPeer(networkName string, peerName string) (peer *api.NetworkPeer, ETag string, err error)
	CreateNetworkPeer(networkName string, peer api.NetworkPeersPost) error
	UpdateNetworkPeer(networkName string, peerName string, peer api.NetworkPeerPut, ETag string) (err error)
	DeleteNetworkPeer(networkName string, peerName string) (err error)

	// Network ACL functions ("network_acl" API extension)
	GetNetworkACLNames() (names []string, err error)
	GetNetworkACLs() (acls []api.NetworkACL, err error)
	GetNetworkACL(name string) (acl *api.NetworkACL, ETag string, err error)
	GetNetworkACLLogfile(name string) (log io.ReadCloser, err error)
	CreateNetworkACL(acl api.NetworkACLsPost) (err error)
	UpdateNetworkACL(name string, acl api.NetworkACLPut, ETag string) (err error)
	RenameNetworkACL(name string, acl api.NetworkACLPost) (err error)
	DeleteNetworkACL(name string) (err error)

	// Network allocations functions ("network_allocations" API extension)
	GetNetworkAllocations(allProjects bool) (allocations []api.NetworkAllocations, err error)

	// Network zone functions ("network_dns" API extension)
	GetNetworkZoneNames() (names []string, err error)
	GetNetworkZones() (zones []api.NetworkZone, err error)
	GetNetworkZone(name string) (zone *api.NetworkZone, ETag string, err error)
	CreateNetworkZone(zone api.NetworkZonesPost) (err error)
	UpdateNetworkZone(name string, zone api.NetworkZonePut, ETag string) (err error)
	DeleteNetworkZone(name string) (err error)

	GetNetworkZoneRecordNames(zone string) (names []string, err error)
	GetNetworkZoneRecords(zone string) (records []api.NetworkZoneRecord, err error)
	GetNetworkZoneRecord(zone string, name string) (record *api.NetworkZoneRecord, ETag string, err error)
	CreateNetworkZoneRecord(zone string, record api.NetworkZoneRecordsPost) (err error)
	UpdateNetworkZoneRecord(zone string, name string, record api.NetworkZoneRecordPut, ETag string) (err error)
	DeleteNetworkZoneRecord(zone string, name string) (err error)

	// Network integrations functions ("network_integrations" API extension)
	GetNetworkIntegrationNames() (names []string, err error)
	GetNetworkIntegrations() (integrations []api.NetworkIntegration, err error)
	GetNetworkIntegration(name string) (integration *api.NetworkIntegration, ETag string, err error)
	CreateNetworkIntegration(integration api.NetworkIntegrationsPost) (err error)
	UpdateNetworkIntegration(name string, integration api.NetworkIntegrationPut, ETag string) (err error)
	RenameNetworkIntegration(name string, integration api.NetworkIntegrationPost) (err error)
	DeleteNetworkIntegration(name string) (err error)

	// Operation functions
	GetOperationUUIDs() (uuids []string, err error)
	GetOperations() (operations []api.Operation, err error)
	GetOperationsAllProjects() (operations []api.Operation, err error)
	GetOperation(uuid string) (op *api.Operation, ETag string, err error)
	GetOperationWait(uuid string, timeout int) (op *api.Operation, ETag string, err error)
	GetOperationWaitSecret(uuid string, secret string, timeout int) (op *api.Operation, ETag string, err error)
	GetOperationWebsocket(uuid string, secret string) (conn *websocket.Conn, err error)
	DeleteOperation(uuid string) (err error)

	// Profile functions
	GetProfileNames() (names []string, err error)
	GetProfiles() (profiles []api.Profile, err error)
	GetProfile(name string) (profile *api.Profile, ETag string, err error)
	CreateProfile(profile api.ProfilesPost) (err error)
	UpdateProfile(name string, profile api.ProfilePut, ETag string) (err error)
	RenameProfile(name string, profile api.ProfilePost) (err error)
	DeleteProfile(name string) (err error)

	// Project functions
	GetProjectNames() (names []string, err error)
	GetProjects() (projects []api.Project, err error)
	GetProject(name string) (project *api.Project, ETag string, err error)
	GetProjectState(name string) (project *api.ProjectState, err error)
	CreateProject(project api.ProjectsPost) (err error)
	UpdateProject(name string, project api.ProjectPut, ETag string) (err error)
	RenameProject(name string, project api.ProjectPost) (op Operation, err error)
	DeleteProject(name string) (err error)

	// Storage pool functions ("storage" API extension)
	GetStoragePoolNames() (names []string, err error)
	GetStoragePools() (pools []api.StoragePool, err error)
	GetStoragePool(name string) (pool *api.StoragePool, ETag string, err error)
	GetStoragePoolResources(name string) (resources *api.ResourcesStoragePool, err error)
	CreateStoragePool(pool api.StoragePoolsPost) (err error)
	UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) (err error)
	DeleteStoragePool(name string) (err error)

	// Storage bucket functions ("storage_buckets" API extension)
	GetStoragePoolBucketNames(poolName string) ([]string, error)
	GetStoragePoolBuckets(poolName string) ([]api.StorageBucket, error)
	GetStoragePoolBucket(poolName string, bucketName string) (bucket *api.StorageBucket, ETag string, err error)
	CreateStoragePoolBucket(poolName string, bucket api.StorageBucketsPost) (*api.StorageBucketKey, error)
	UpdateStoragePoolBucket(poolName string, bucketName string, bucket api.StorageBucketPut, ETag string) (err error)
	DeleteStoragePoolBucket(poolName string, bucketName string) (err error)
	GetStoragePoolBucketKeyNames(poolName string, bucketName string) ([]string, error)
	GetStoragePoolBucketKeys(poolName string, bucketName string) ([]api.StorageBucketKey, error)
	GetStoragePoolBucketKey(poolName string, bucketName string, keyName string) (key *api.StorageBucketKey, ETag string, err error)
	CreateStoragePoolBucketKey(poolName string, bucketName string, key api.StorageBucketKeysPost) (newKey *api.StorageBucketKey, err error)
	UpdateStoragePoolBucketKey(poolName string, bucketName string, keyName string, key api.StorageBucketKeyPut, ETag string) (err error)
	DeleteStoragePoolBucketKey(poolName string, bucketName string, keyName string) (err error)

	// Storage bucket backup functions ("storage_bucket_backup" API extension)
	CreateStoragePoolBucketBackup(poolName string, bucketName string, backup api.StorageBucketBackupsPost) (op Operation, err error)
	DeleteStoragePoolBucketBackup(pool string, bucketName string, name string) (op Operation, err error)
	GetStoragePoolBucketBackupFile(pool string, bucketName string, name string, req *BackupFileRequest) (resp *BackupFileResponse, err error)
	CreateStoragePoolBucketFromBackup(pool string, args StoragePoolBucketBackupArgs) (op Operation, err error)

	// Storage volume functions ("storage" API extension)
	GetStoragePoolVolumeNames(pool string) (names []string, err error)
	GetStoragePoolVolumeNamesAllProjects(pool string) (names map[string][]string, err error)
	GetStoragePoolVolumes(pool string) (volumes []api.StorageVolume, err error)
	GetStoragePoolVolumesAllProjects(pool string) (volumes []api.StorageVolume, err error)
	GetStoragePoolVolumesWithFilter(pool string, filters []string) (volumes []api.StorageVolume, err error)
	GetStoragePoolVolumesWithFilterAllProjects(pool string, filters []string) (volumes []api.StorageVolume, err error)
	GetStoragePoolVolume(pool string, volType string, name string) (volume *api.StorageVolume, ETag string, err error)
	GetStoragePoolVolumeState(pool string, volType string, name string) (state *api.StorageVolumeState, err error)
	CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) (err error)
	UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) (err error)
	DeleteStoragePoolVolume(pool string, volType string, name string) (err error)
	RenameStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePost) (err error)
	CopyStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (op RemoteOperation, err error)
	MoveStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (op RemoteOperation, err error)
	MigrateStoragePoolVolume(pool string, volume api.StorageVolumePost) (op Operation, err error)

	// Storage volume snapshot functions ("storage_api_volume_snapshots" API extension)
	CreateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshot api.StorageVolumeSnapshotsPost) (op Operation, err error)
	DeleteStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (op Operation, err error)
	GetStoragePoolVolumeSnapshotNames(pool string, volumeType string, volumeName string) (names []string, err error)
	GetStoragePoolVolumeSnapshots(pool string, volumeType string, volumeName string) (snapshots []api.StorageVolumeSnapshot, err error)
	GetStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (snapshot *api.StorageVolumeSnapshot, ETag string, err error)
	RenameStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, snapshot api.StorageVolumeSnapshotPost) (op Operation, err error)
	UpdateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, volume api.StorageVolumeSnapshotPut, ETag string) (err error)

	// Storage volume backup functions ("custom_volume_backup" API extension)
	GetStoragePoolVolumeBackupNames(pool string, volName string) (names []string, err error)
	GetStoragePoolVolumeBackups(pool string, volName string) (backups []api.StoragePoolVolumeBackup, err error)
	GetStoragePoolVolumeBackup(pool string, volName string, name string) (backup *api.StoragePoolVolumeBackup, ETag string, err error)
	CreateStoragePoolVolumeBackup(pool string, volName string, backup api.StoragePoolVolumeBackupsPost) (op Operation, err error)
	RenameStoragePoolVolumeBackup(pool string, volName string, name string, backup api.StoragePoolVolumeBackupPost) (op Operation, err error)
	DeleteStoragePoolVolumeBackup(pool string, volName string, name string) (op Operation, err error)
	GetStoragePoolVolumeBackupFile(pool string, volName string, name string, req *BackupFileRequest) (resp *BackupFileResponse, err error)
	CreateStoragePoolVolumeFromBackup(pool string, args StoragePoolVolumeBackupArgs) (op Operation, err error)

	// Storage volume ISO import function ("custom_volume_iso" API extension)
	CreateStoragePoolVolumeFromISO(pool string, args StoragePoolVolumeBackupArgs) (op Operation, err error)

	// Cluster functions ("cluster" API extensions)
	GetCluster() (cluster *api.Cluster, ETag string, err error)
	UpdateCluster(cluster api.ClusterPut, ETag string) (op Operation, err error)
	DeleteClusterMember(name string, force bool) (err error)
	GetClusterMemberNames() (names []string, err error)
	GetClusterMembers() (members []api.ClusterMember, err error)
	GetClusterMember(name string) (member *api.ClusterMember, ETag string, err error)
	UpdateClusterMember(name string, member api.ClusterMemberPut, ETag string) (err error)
	RenameClusterMember(name string, member api.ClusterMemberPost) (err error)
	CreateClusterMember(member api.ClusterMembersPost) (op Operation, err error)
	UpdateClusterCertificate(certs api.ClusterCertificatePut, ETag string) (err error)
	GetClusterMemberState(name string) (*api.ClusterMemberState, string, error)
	UpdateClusterMemberState(name string, state api.ClusterMemberStatePost) (op Operation, err error)
	GetClusterGroups() ([]api.ClusterGroup, error)
	GetClusterGroupNames() ([]string, error)
	RenameClusterGroup(name string, group api.ClusterGroupPost) error
	CreateClusterGroup(group api.ClusterGroupsPost) error
	DeleteClusterGroup(name string) error
	UpdateClusterGroup(name string, group api.ClusterGroupPut, ETag string) error
	GetClusterGroup(name string) (*api.ClusterGroup, string, error)

	// Warning functions
	GetWarningUUIDs() (uuids []string, err error)
	GetWarnings() (warnings []api.Warning, err error)
	GetWarning(UUID string) (warning *api.Warning, ETag string, err error)
	UpdateWarning(UUID string, warning api.WarningPut, ETag string) (err error)
	DeleteWarning(UUID string) (err error)

	// Internal functions (for internal use)
	RawQuery(method string, path string, data any, queryETag string) (resp *api.Response, ETag string, err error)
	RawWebsocket(path string) (conn *websocket.Conn, err error)
	RawOperation(method string, path string, data any, queryETag string) (op Operation, ETag string, err error)
}

// The ConnectionInfo struct represents general information for a connection.
type ConnectionInfo struct {
	Addresses   []string
	Certificate string
	Protocol    string
	URL         string
	SocketPath  string
	Project     string
	Target      string
}

// The BackupFileRequest struct is used for a backup download request.
type BackupFileRequest struct {
	// Writer for the backup file
	BackupFile io.WriteSeeker

	// Progress handler (called whenever some progress is made)
	ProgressHandler func(progress ioprogress.ProgressData)

	// A canceler that can be used to interrupt some part of the image download request
	Canceler *cancel.HTTPRequestCanceller
}

// The BackupFileResponse struct is used as the response for backup downloads.
type BackupFileResponse struct {
	// Size of backup file
	Size int64
}

// The ImageCreateArgs struct is used for direct image upload.
type ImageCreateArgs struct {
	// Reader for the meta file
	MetaFile io.Reader

	// Filename for the meta file
	MetaName string

	// Reader for the rootfs file
	RootfsFile io.Reader

	// Filename for the rootfs file
	RootfsName string

	// Progress handler (called with upload progress)
	ProgressHandler func(progress ioprogress.ProgressData)

	// Type of the image (container or virtual-machine)
	Type string
}

// The ImageFileRequest struct is used for an image download request.
type ImageFileRequest struct {
	// Writer for the metadata file
	MetaFile io.WriteSeeker

	// Writer for the rootfs file
	RootfsFile io.WriteSeeker

	// Progress handler (called whenever some progress is made)
	ProgressHandler func(progress ioprogress.ProgressData)

	// A canceler that can be used to interrupt some part of the image download request
	Canceler *cancel.HTTPRequestCanceller

	// Path retriever for image delta downloads
	// If set, it must return the path to the image file or an empty string if not available
	DeltaSourceRetriever func(fingerprint string, file string) string
}

// The ImageFileResponse struct is used as the response for image downloads.
type ImageFileResponse struct {
	// Filename for the metadata file
	MetaName string

	// Size of the metadata file
	MetaSize int64

	// Filename for the rootfs file
	RootfsName string

	// Size of the rootfs file
	RootfsSize int64
}

// The ImageCopyArgs struct is used to pass additional options during image copy.
type ImageCopyArgs struct {
	// Aliases to add to the copied image.
	Aliases []api.ImageAlias

	// Whether to have Incus keep this image up to date
	AutoUpdate bool

	// Whether to copy the source image aliases to the target
	CopyAliases bool

	// Whether this image is to be made available to unauthenticated users
	Public bool

	// The image type to use for resolution
	Type string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// List of profiles to apply on the target.
	Profiles []string
}

// The StoragePoolVolumeCopyArgs struct is used to pass additional options
// during storage volume copy.
type StoragePoolVolumeCopyArgs struct {
	// New name for the target
	Name string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: storage_api_volume_snapshots
	VolumeOnly bool

	// API extension: custom_volume_refresh
	Refresh bool
}

// The StoragePoolVolumeMoveArgs struct is used to pass additional options
// during storage volume move.
type StoragePoolVolumeMoveArgs struct {
	StoragePoolVolumeCopyArgs

	// API extension: storage_volume_project_move
	Project string
}

// The StoragePoolVolumeBackupArgs struct is used when creating a storage volume from a backup.
// API extension: custom_volume_backup.
type StoragePoolVolumeBackupArgs struct {
	// The backup file
	BackupFile io.Reader

	// Name to import backup as
	Name string
}

// The InstanceBackupArgs struct is used when creating a instance from a backup.
type InstanceBackupArgs struct {
	// The backup file
	BackupFile io.Reader

	// Storage pool to use
	PoolName string

	// Name to import backup as
	Name string
}

// The InstanceCopyArgs struct is used to pass additional options during instance copy.
type InstanceCopyArgs struct {
	// If set, the instance will be renamed on copy
	Name string

	// If set, the instance running state will be transferred (live migration)
	Live bool

	// If set, only the instance will copied, its snapshots won't
	InstanceOnly bool

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: container_incremental_copy
	// Perform an incremental copy
	Refresh bool

	// API extension: instance_allow_inconsistent_copy
	AllowInconsistent bool
}

// The InstanceSnapshotCopyArgs struct is used to pass additional options during instance copy.
type InstanceSnapshotCopyArgs struct {
	// If set, the instance will be renamed on copy
	Name string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: container_snapshot_stateful_migration
	// If set, the instance running state will be transferred (live migration)
	Live bool
}

// The InstanceConsoleArgs struct is used to pass additional options during a
// instance console session.
type InstanceConsoleArgs struct {
	// Bidirectional fd to pass to the instance
	Terminal io.ReadWriteCloser

	// Control message handler (window resize)
	Control func(conn *websocket.Conn)

	// Closing this Channel causes a disconnect from the instance's console
	ConsoleDisconnect chan bool
}

// The InstanceConsoleLogArgs struct is used to pass additional options during a
// instance console log request.
type InstanceConsoleLogArgs struct {
}

// The InstanceExecArgs struct is used to pass additional options during instance exec.
type InstanceExecArgs struct {
	// Standard input
	Stdin io.Reader

	// Standard output
	Stdout io.Writer

	// Standard error
	Stderr io.Writer

	// Control message handler (window resize, signals, ...)
	Control func(conn *websocket.Conn)

	// Channel that will be closed when all data operations are done
	DataDone chan bool
}

// The InstanceFileArgs struct is used to pass the various options for a instance file upload.
type InstanceFileArgs struct {
	// File content
	Content io.ReadSeeker

	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// File write mode (overwrite or append)
	WriteMode string
}

// The InstanceFileResponse struct is used as part of the response for a instance file download.
type InstanceFileResponse struct {
	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// If a directory, the list of files inside it
	Entries []string
}

// The StoragePoolBucketBackupArgs struct is used when creating a storage volume from a backup.
// API extension: storage_bucket_backup.
type StoragePoolBucketBackupArgs struct {
	// The backup file
	BackupFile io.Reader

	// Name to import backup as
	Name string
}
