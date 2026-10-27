//go:build linux && cgo && !agent

package cluster

import "context"

// NodeGenerated is an interface of generated methods for Node.
type NodeGenerated interface {
	// GetNodeID return the ID of the node with the given key.
	// generator: node ID
	GetNodeID(ctx context.Context, db tx, name string) (int64, error)
}
