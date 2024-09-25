//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/cowsql/go-cowsql/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/response"
)

// Fetch all raft nodes.
func TestRaftNodes(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id1, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	require.NoError(t, err)

	id2, err := tx.CreateRaftNode("5.6.7.8:666", "test")
	require.NoError(t, err)

	nodes, err := tx.GetRaftNodes(context.Background())
	require.NoError(t, err)

	assert.Equal(t, uint64(id1), nodes[0].ID)
	assert.Equal(t, uint64(id2), nodes[1].ID)
	assert.Equal(t, "1.2.3.4:666", nodes[0].Address)
	assert.Equal(t, "5.6.7.8:666", nodes[1].Address)
}

// Fetch the addresses of all raft nodes.
func TestGetRaftNodeAddresses(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	require.NoError(t, err)

	_, err = tx.CreateRaftNode("5.6.7.8:666", "test")
	require.NoError(t, err)

	addresses, err := tx.GetRaftNodeAddresses(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"1.2.3.4:666", "5.6.7.8:666"}, addresses)
}

// Fetch the address of the raft node with the given ID.
func TestGetRaftNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	require.NoError(t, err)

	id, err := tx.CreateRaftNode("5.6.7.8:666", "test")
	require.NoError(t, err)

	address, err := tx.GetRaftNodeAddress(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add the first raft node.
func TestCreateFirstRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.CreateFirstRaftNode("1.2.3.4:666", "test")
	assert.NoError(t, err)

	err = tx.RemoveRaftNode(1)
	assert.NoError(t, err)

	err = tx.CreateFirstRaftNode("5.6.7.8:666", "test")
	assert.NoError(t, err)

	address, err := tx.GetRaftNodeAddress(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add a new raft node.
func TestCreateRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	assert.Equal(t, int64(1), id)
	assert.NoError(t, err)
}

// Delete an existing raft node.
func TestRemoveRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	require.NoError(t, err)

	err = tx.RemoveRaftNode(id)
	assert.NoError(t, err)
}

// Delete a non-existing raft node returns an error.
func TestRemoveRaftNode_NonExisting(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.RemoveRaftNode(1)
	assert.True(t, response.IsNotFoundError(err))
}

// Replace all existing raft nodes.
func TestReplaceRaftNodes(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666", "test")
	require.NoError(t, err)

	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 2, Address: "2.2.2.2:666"}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "3.3.3.3:666"}},
	}

	err = tx.ReplaceRaftNodes(nodes)
	assert.NoError(t, err)

	newNodes, err := tx.GetRaftNodes(context.Background())
	require.NoError(t, err)

	assert.Equal(t, nodes, newNodes)
}
