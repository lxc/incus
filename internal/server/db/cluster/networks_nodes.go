//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//generate-database:mapper target networks_nodes.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e network_node objects table=networks_nodes
//
// Methods:
//generate-database:mapper method -i -e network_node GetMany table=networks_nodes

// NetworkNode is a value object holding db-related details about a network's per-member state.
type NetworkNode struct {
	ID        int64  `db:"order=yes"`
	NetworkID int64  `db:"primary=yes&column=network_id"`
	NodeID    int64  `db:"primary=yes&column=node_id"`
	Name      string `db:"join=nodes.name"`
	State     NetworkState
}

// NetworkNodeFilter specifies potential query parameter fields.
type NetworkNodeFilter struct {
	NetworkID *int64
	NodeID    *int64
}
