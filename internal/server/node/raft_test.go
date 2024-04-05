package node_test

import (
	"context"
	"testing"

	"github.com/cowsql/go-cowsql/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
)

// The raft identity (ID and address) of a node depends on the value of
// cluster.https_address and the entries of the raft_nodes table.
func TestDetermineRaftNode(t *testing.T) {
	cases := []struct {
		title     string
		address   string       // Value of cluster.https_address
		addresses []string     // Entries in raft_nodes
		node      *db.RaftNode // Expected node value
	}{
		{
			`no cluster.https_address set`,
			"",
			[]string{},
			&db.RaftNode{NodeInfo: client.NodeInfo{ID: 1}},
		},
		{
			`cluster.https_address set and no raft_nodes rows`,
			"1.2.3.4:8443",
			[]string{},
			&db.RaftNode{NodeInfo: client.NodeInfo{ID: 1}},
		},
		{
			`cluster.https_address set and matching the one and only raft_nodes row`,
			"1.2.3.4:8443",
			[]string{"1.2.3.4:8443"},
			&db.RaftNode{NodeInfo: client.NodeInfo{ID: 1, Address: "1.2.3.4:8443"}},
		},
		{
			`cluster.https_address set and matching one of many raft_nodes rows`,
			"5.6.7.8:999",
			[]string{"1.2.3.4:666", "5.6.7.8:999"},
			&db.RaftNode{NodeInfo: client.NodeInfo{ID: 2, Address: "5.6.7.8:999"}},
		},
		{
			`core.cluster set and no matching raft_nodes row`,
			"1.2.3.4:666",
			[]string{"5.6.7.8:999"},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			tx, cleanup := db.NewTestNodeTx(t)
			defer cleanup()

			err := tx.UpdateConfig(map[string]string{"cluster.https_address": c.address})
			require.NoError(t, err)

			for _, address := range c.addresses {
				_, err := tx.CreateRaftNode(address, "test")
				require.NoError(t, err)
			}

			node, err := node.DetermineRaftNode(context.Background(), tx)
			require.NoError(t, err)
			if c.node == nil {
				assert.Nil(t, node)
			} else {
				assert.Equal(t, c.node.ID, node.ID)
				assert.Equal(t, c.node.Address, node.Address)
			}
		})
	}
}
