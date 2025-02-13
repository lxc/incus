package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

//go:generate -command mapper generate-database db mapper -t cluster_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e cluster_group objects table=cluster_groups
//go:generate mapper stmt -e cluster_group objects-by-Name table=cluster_groups
//go:generate mapper stmt -e cluster_group id table=cluster_groups
//go:generate mapper stmt -e cluster_group create table=cluster_groups
//go:generate mapper stmt -e cluster_group rename table=cluster_groups
//go:generate mapper stmt -e cluster_group delete-by-Name table=cluster_groups
//go:generate mapper stmt -e cluster_group update table=cluster_groups
//
//go:generate mapper method -i -e cluster_group GetMany references=Config
//go:generate mapper method -i -e cluster_group GetOne
//go:generate mapper method -i -e cluster_group ID
//go:generate mapper method -i -e cluster_group Exists
//go:generate mapper method -i -e cluster_group Rename
//go:generate mapper method -i -e cluster_group Create references=Config
//go:generate mapper method -i -e cluster_group Update references=Config
//go:generate mapper method -i -e cluster_group DeleteOne-by-Name

// ClusterGroup is a value object holding db-related details about a cluster group.
type ClusterGroup struct {
	ID          int
	Name        string
	Description string   `db:"coalesce=''"`
	Nodes       []string `db:"ignore"`
}

// ClusterGroupFilter specifies potential query parameter fields.
type ClusterGroupFilter struct {
	ID   *int
	Name *string
}

// ToAPI returns an API entry.
func (c *ClusterGroup) ToAPI(ctx context.Context, tx *sql.Tx) (*api.ClusterGroup, error) {
	// Get the config.
	config, err := GetClusterGroupConfig(ctx, tx, c.ID)
	if err != nil {
		return nil, err
	}

	result := api.ClusterGroup{
		ClusterGroupPut: api.ClusterGroupPut{
			Config:      config,
			Description: c.Description,
			Members:     c.Nodes,
		},
		ClusterGroupPost: api.ClusterGroupPost{
			Name: c.Name,
		},
	}

	return &result, nil
}
