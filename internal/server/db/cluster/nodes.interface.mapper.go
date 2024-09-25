//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// NodeGenerated is an interface of generated methods for Node.
type NodeGenerated interface {
	// GetNodeID return the ID of the node with the given key.
	// generator: node ID
	GetNodeID(ctx context.Context, tx *sql.Tx, name string) (int64, error)
}
