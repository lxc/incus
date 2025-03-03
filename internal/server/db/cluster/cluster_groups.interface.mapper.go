//go:build linux && cgo && !agent

package cluster

import "context"

// ClusterGroupGenerated is an interface of generated methods for ClusterGroup.
type ClusterGroupGenerated interface {
	// GetClusterGroupConfig returns all available ClusterGroup Config
	// generator: cluster_group GetMany
	GetClusterGroupConfig(ctx context.Context, db dbtx, clusterGroupID int, filters ...ConfigFilter) (map[string]string, error)

	// GetClusterGroups returns all available cluster_groups.
	// generator: cluster_group GetMany
	GetClusterGroups(ctx context.Context, db dbtx, filters ...ClusterGroupFilter) ([]ClusterGroup, error)

	// GetClusterGroup returns the cluster_group with the given key.
	// generator: cluster_group GetOne
	GetClusterGroup(ctx context.Context, db dbtx, name string) (*ClusterGroup, error)

	// GetClusterGroupID return the ID of the cluster_group with the given key.
	// generator: cluster_group ID
	GetClusterGroupID(ctx context.Context, db dbtx, name string) (int64, error)

	// ClusterGroupExists checks if a cluster_group with the given key exists.
	// generator: cluster_group Exists
	ClusterGroupExists(ctx context.Context, db dbtx, name string) (bool, error)

	// RenameClusterGroup renames the cluster_group matching the given key parameters.
	// generator: cluster_group Rename
	RenameClusterGroup(ctx context.Context, db dbtx, name string, to string) error

	// CreateClusterGroupConfig adds new cluster_group Config to the database.
	// generator: cluster_group Create
	CreateClusterGroupConfig(ctx context.Context, db dbtx, clusterGroupID int64, config map[string]string) error

	// CreateClusterGroup adds a new cluster_group to the database.
	// generator: cluster_group Create
	CreateClusterGroup(ctx context.Context, db dbtx, object ClusterGroup) (int64, error)

	// UpdateClusterGroupConfig updates the cluster_group Config matching the given key parameters.
	// generator: cluster_group Update
	UpdateClusterGroupConfig(ctx context.Context, db dbtx, clusterGroupID int64, config map[string]string) error

	// UpdateClusterGroup updates the cluster_group matching the given key parameters.
	// generator: cluster_group Update
	UpdateClusterGroup(ctx context.Context, db dbtx, name string, object ClusterGroup) error

	// DeleteClusterGroup deletes the cluster_group matching the given key parameters.
	// generator: cluster_group DeleteOne-by-Name
	DeleteClusterGroup(ctx context.Context, db dbtx, name string) error
}
