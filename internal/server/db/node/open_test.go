package node_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/node"
)

func TestOpen(t *testing.T) {
	dir, cleanup := newDir(t)
	defer cleanup()

	db, err := node.Open(dir)
	defer func() { _ = db.Close() }()
	require.NoError(t, err)
}

// When the node-local database is created from scratch, the value for the
// initial patch is 0.
func TestEnsureSchema(t *testing.T) {
	dir, cleanup := newDir(t)
	defer cleanup()

	db, err := node.Open(dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	initial, err := node.EnsureSchema(db, dir)
	require.NoError(t, err)
	assert.Equal(t, 0, initial)
}

// Create a new temporary directory, along with a function to clean it up.
func newDir(t *testing.T) (string, func()) {
	dir, err := os.MkdirTemp("", "incus-db-node-test-")
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, os.RemoveAll(dir))
	}

	return dir, cleanup
}
