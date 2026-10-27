package cluster

// Code generation directives.
//
//generate-database:mapper target nodes.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e node id
//
//generate-database:mapper method -i -e node ID

// Node represents a cluster member.
type Node struct {
	ID   int
	Name string
}

// NodeFilter specifies potential query parameter fields.
type NodeFilter struct {
	Name *string
}
