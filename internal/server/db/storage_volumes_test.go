//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
)

// Addresses of all nodes with matching volume name are returned.
func TestGetStorageVolumeNodes(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.CreateNode("node3", "5.6.7.8:666")
	require.NoError(t, err)

	poolID := addPool(t, tx, "pool1")
	addVolume(t, tx, poolID, nodeID1, "volume1")
	addVolume(t, tx, poolID, nodeID2, "volume1")
	addVolume(t, tx, poolID, nodeID3, "volume2")
	addVolume(t, tx, poolID, nodeID2, "volume2")

	nodes, err := tx.GetStorageVolumeNodes(context.Background(), poolID, "default", "volume1", 1)
	require.NoError(t, err)

	assert.Equal(t, []db.NodeInfo{
		{
			ID:      nodeID1,
			Name:    "none",
			Address: "0.0.0.0",
		},
		{
			ID:      nodeID2,
			Name:    "node2",
			Address: "1.2.3.4:666",
		},
	}, nodes)
}

func addPool(t *testing.T, tx *db.ClusterTx, name string) int64 {
	stmt := `
INSERT INTO storage_pools(name, driver, description) VALUES (?, 'dir', '')
`
	result, err := tx.Tx().Exec(stmt, name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

func addVolume(t *testing.T, tx *db.ClusterTx, poolID, nodeID int64, name string) {
	stmt := `
INSERT INTO storage_volumes(storage_pool_id, node_id, name, type, project_id, description) VALUES (?, ?, ?, 1, 1, '')
`
	_, err := tx.Tx().Exec(stmt, poolID, nodeID, name)
	require.NoError(t, err)
}
