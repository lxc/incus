//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
)

// NodeTx models a single interaction with a server-local database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type NodeTx struct {
	*sql.Tx // Handle to a transaction in the node-level SQLite database.
}

// ClusterTx models a single interaction with a cluster database.
//
// It wraps low-level sql.Tx objects and offers a high-level API to fetch and
// update data.
type ClusterTx struct {
	tx     *sql.Tx // Handle to a transaction in the cluster cowsql database.
	nodeID int64   // Node ID of this server.
}

// Commit implements [transaction.TX].
func (c *ClusterTx) Commit() error {
	return c.tx.Commit()
}

// ExecContext implements [transaction.TX].
func (c *ClusterTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.tx.ExecContext(ctx, query, args...)
}

// PrepareContext implements [transaction.TX].
func (c *ClusterTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return c.tx.PrepareContext(ctx, query)
}

// QueryContext implements [transaction.TX].
func (c *ClusterTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return c.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext implements [transaction.TX].
func (c *ClusterTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return c.tx.QueryRowContext(ctx, query, args)
}

// Rollback implements [transaction.TX].
func (c *ClusterTx) Rollback() error {
	return c.tx.Rollback()
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
