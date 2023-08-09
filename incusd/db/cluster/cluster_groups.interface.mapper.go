//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ClusterGroupGenerated is an interface of generated methods for ClusterGroup.
type ClusterGroupGenerated interface {
	// GetClusterGroups returns all available cluster_groups.
	// generator: cluster_group GetMany
	GetClusterGroups(ctx context.Context, tx *sql.Tx, filters ...ClusterGroupFilter) ([]ClusterGroup, error)

	// GetClusterGroup returns the cluster_group with the given key.
	// generator: cluster_group GetOne
	GetClusterGroup(ctx context.Context, tx *sql.Tx, name string) (*ClusterGroup, error)

	// GetClusterGroupID return the ID of the cluster_group with the given key.
	// generator: cluster_group ID
	GetClusterGroupID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// ClusterGroupExists checks if a cluster_group with the given key exists.
	// generator: cluster_group Exists
	ClusterGroupExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// RenameClusterGroup renames the cluster_group matching the given key parameters.
	// generator: cluster_group Rename
	RenameClusterGroup(ctx context.Context, tx *sql.Tx, name string, to string) error

	// CreateClusterGroup adds a new cluster_group to the database.
	// generator: cluster_group Create
	CreateClusterGroup(ctx context.Context, tx *sql.Tx, object ClusterGroup) (int64, error)

	// UpdateClusterGroup updates the cluster_group matching the given key parameters.
	// generator: cluster_group Update
	UpdateClusterGroup(ctx context.Context, tx *sql.Tx, name string, object ClusterGroup) error

	// DeleteClusterGroup deletes the cluster_group matching the given key parameters.
	// generator: cluster_group DeleteOne-by-Name
	DeleteClusterGroup(ctx context.Context, tx *sql.Tx, name string) error
}
