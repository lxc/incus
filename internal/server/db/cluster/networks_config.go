//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//generate-database:mapper target networks_config.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e network_config objects table=networks_config
//generate-database:mapper stmt -e network_config objects-by-NodeID table=networks_config
//generate-database:mapper stmt -e network_config objects-by-NodeID-and-NetworkID table=networks_config
//
// Methods:
//generate-database:mapper method -i -e network_config GetMany table=networks_config

// NetworkConfig is a value object holding db-related details about a network config entry.
type NetworkConfig struct {
	ID        int64  `db:"order=yes"`
	NetworkID int64  `db:"primary=yes&column=network_id"`
	NodeID    int64  `db:"coalesce=0&column=node_id"`
	Key       string `db:"primary=yes"`
	Value     string
}

// NetworkConfigFilter specifies potential query parameter fields.
type NetworkConfigFilter struct {
	NetworkID *int64
	NodeID    *int64
}
