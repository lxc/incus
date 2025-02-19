package cluster

// Code generation directives.
//
//go:generate -command mapper generate-database db mapper -t nodes_cluster_groups.mapper.go
//go:generate mapper generate -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt node_cluster_group objects table=nodes_cluster_groups
//generate-database:mapper stmt node_cluster_group objects-by-GroupID table=nodes_cluster_groups
//generate-database:mapper stmt node_cluster_group id table=nodes_cluster_groups
//generate-database:mapper stmt node_cluster_group create table=nodes_cluster_groups
//generate-database:mapper stmt node_cluster_group delete-by-GroupID table=nodes_cluster_groups
//
//generate-database:mapper method node_cluster_group GetMany
//generate-database:mapper method node_cluster_group Create
//generate-database:mapper method node_cluster_group Exists
//generate-database:mapper method node_cluster_group ID
//generate-database:mapper method node_cluster_group DeleteOne-by-GroupID

// NodeClusterGroup associates a node to a cluster group.
type NodeClusterGroup struct {
	GroupID int    `db:"primary=yes"`
	Node    string `db:"join=nodes.name"`
	NodeID  int    `db:"omit=create,objects,objects-by-GroupID"`
}

// NodeClusterGroupFilter specifies potential query parameter fields.
type NodeClusterGroupFilter struct {
	GroupID *int
}
