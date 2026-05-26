//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/query"
)

// Node database objects automatically initialize their schema as needed.
func TestNode_Schema(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	// The underlying node-level database has exactly one row in the schema
	// table.
	dbHandle := node.DB()
	tx, err := dbHandle.Begin()
	require.NoError(t, err)
	n, err := query.Count(context.Background(), tx, "schema", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.NoError(t, tx.Commit())
	assert.NoError(t, dbHandle.Close())
}

// A gRPC SQL connection is established when starting to interact with the
// cluster database.
func TestCluster_Setup(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// The underlying node-level database has exactly one row in the schema
	// table.
	dbHandle := cluster.DB()
	tx, err := dbHandle.Begin()
	require.NoError(t, err)
	n, err := query.Count(context.Background(), tx, "schema", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.NoError(t, tx.Commit())
	assert.NoError(t, dbHandle.Close())
}
