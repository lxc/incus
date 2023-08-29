//go:build linux && cgo && !agent

package db

import (
	"database/sql"
)

// NodeTx models a single interaction with a server-local database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type NodeTx struct {
	tx *sql.Tx // Handle to a transaction in the node-level SQLite database.
}

// ClusterTx models a single interaction with a cluster database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type ClusterTx struct {
	tx     *sql.Tx // Handle to a transaction in the cluster dqlite database.
	nodeID int64   // Node ID of this server.
}

// Tx retrieves the underlying transaction on the cluster database.
func (c *ClusterTx) Tx() *sql.Tx {
	return c.tx
}

// NodeID sets the node NodeID associated with this cluster transaction.
func (c *ClusterTx) NodeID(id int64) {
	c.nodeID = id
}

// GetNodeID gets the ID of the node associated with this cluster transaction.
func (c *ClusterTx) GetNodeID() int64 {
	return c.nodeID
}
