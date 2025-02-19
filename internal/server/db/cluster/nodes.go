package cluster

//go:generate -command mapper generate-database db mapper -t nodes.mapper.go
//go:generate mapper generate -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt node id
//
//generate-database:mapper method node ID

// Node represents a cluster member.
type Node struct {
	ID   int
	Name string
}

// NodeFilter specifies potential query parameter fields.
type NodeFilter struct {
	Name *string
}
