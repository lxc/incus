//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//generate-database:mapper target networks.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e network objects
//generate-database:mapper stmt -e network objects-by-State
//generate-database:mapper stmt -e network objects-by-Project-and-State
//
// Methods:
//generate-database:mapper method -i -e network GetMany

// NetworkState indicates the state of the network or network node.
type NetworkState int

// NetworkType indicates the type of network.
type NetworkType int

// Network is a value object holding db-related details about a network.
type Network struct {
	ID          int64  `db:"order=yes"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string
	Type        NetworkType
	State       NetworkState
}

// NetworkFilter specifies potential query parameter fields.
type NetworkFilter struct {
	ID      *int64
	Project *string
	Name    *string
	Type    *NetworkType
	State   *NetworkState
}
