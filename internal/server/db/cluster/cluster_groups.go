package cluster

import (
	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target cluster_groups.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e cluster_group objects table=cluster_groups
//generate-database:mapper stmt -e cluster_group objects-by-Name table=cluster_groups
//generate-database:mapper stmt -e cluster_group id table=cluster_groups
//generate-database:mapper stmt -e cluster_group create table=cluster_groups
//generate-database:mapper stmt -e cluster_group rename table=cluster_groups
//generate-database:mapper stmt -e cluster_group delete-by-Name table=cluster_groups
//generate-database:mapper stmt -e cluster_group update table=cluster_groups
//
//generate-database:mapper method -i -e cluster_group GetMany table=cluster_groups
//generate-database:mapper method -i -e cluster_group GetOne table=cluster_groups
//generate-database:mapper method -i -e cluster_group ID table=cluster_groups
//generate-database:mapper method -i -e cluster_group Exists table=cluster_groups
//generate-database:mapper method -i -e cluster_group Rename table=cluster_groups
//generate-database:mapper method -i -e cluster_group Create table=cluster_groups
//generate-database:mapper method -i -e cluster_group Update table=cluster_groups
//generate-database:mapper method -i -e cluster_group DeleteOne-by-Name table=cluster_groups

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
func (c *ClusterGroup) ToAPI() (*api.ClusterGroup, error) {
	result := api.ClusterGroup{
		ClusterGroupPut: api.ClusterGroupPut{
			Description: c.Description,
			Members:     c.Nodes,
		},
		ClusterGroupPost: api.ClusterGroupPost{
			Name: c.Name,
		},
	}

	return &result, nil
}
