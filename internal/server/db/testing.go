//go:build linux && cgo && !agent

package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	dqlite "github.com/cowsql/go-cowsql"
	"github.com/cowsql/go-cowsql/client"
	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewTestNode creates a new Node for testing purposes, along with a function
// that can be used to clean it up when done.
func NewTestNode(t *testing.T) (*Node, func()) {
	dir, err := os.MkdirTemp("", "incus-db-test-node-")
	require.NoError(t, err)

	db, err := OpenNode(dir, nil)
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, db.Close())
		require.NoError(t, os.RemoveAll(dir))
	}

	return db, cleanup
}

// NewTestNodeTx returns a fresh NodeTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestNodeTx(t *testing.T) (*NodeTx, func()) {
	node, nodeCleanup := NewTestNode(t)

	var err error

	nodeTx := &NodeTx{}
	nodeTx.tx, err = node.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, nodeTx.tx.Commit())
		nodeCleanup()
	}

	return nodeTx, cleanup
}

// NewTestCluster creates a new Cluster for testing purposes, along with a function
// that can be used to clean it up when done.
func NewTestCluster(t *testing.T) (*Cluster, func()) {
	// Create an in-memory dqlite SQL server and associated store.
	dir, store, serverCleanup := NewTestDqliteServer(t)

	log := newLogFunc(t)

	dial := func(ctx context.Context, address string) (net.Conn, error) {
		return net.Dial("unix", address)
	}

	cluster, err := OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, driver.WithLogFunc(log), driver.WithDialFunc(dial))
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, cluster.Close())
		serverCleanup()
	}

	return cluster, cleanup
}

// NewTestClusterTx returns a fresh ClusterTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestClusterTx(t *testing.T) (*ClusterTx, func()) {
	cluster, clusterCleanup := NewTestCluster(t)

	var err error

	clusterTx := &ClusterTx{nodeID: cluster.nodeID}
	clusterTx.tx, err = cluster.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		err := clusterTx.tx.Commit()
		require.NoError(t, err)
		clusterCleanup()
	}

	return clusterTx, cleanup
}

// NewTestDqliteServer creates a new test dqlite server.
//
// Return the directory backing the test server and a newly created server
// store that can be used to connect to it.
func NewTestDqliteServer(t *testing.T) (string, driver.NodeStore, func()) {
	t.Helper()

	listener, err := net.Listen("unix", "")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	dir, dirCleanup := newDir(t)
	err = os.Mkdir(filepath.Join(dir, "global"), 0o755)
	require.NoError(t, err)

	server, err := dqlite.New(
		uint64(1), address, filepath.Join(dir, "global"), dqlite.WithBindAddress(address))
	require.NoError(t, err)

	err = server.Start()
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, server.Close())
		dirCleanup()
	}

	store, err := driver.DefaultNodeStore(":memory:")
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, []driver.NodeInfo{{Address: address}}))

	return dir, store, cleanup
}

// Return a new temporary directory.
func newDir(t *testing.T) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "dqlite-replication-test-")
	assert.NoError(t, err)

	cleanup := func() {
		_, err := os.Stat(dir)
		if err != nil {
			assert.True(t, errors.Is(err, fs.ErrNotExist))
		} else {
			assert.NoError(t, os.RemoveAll(dir))
		}
	}

	return dir, cleanup
}

func newLogFunc(t *testing.T) client.LogFunc {
	return func(l client.LogLevel, format string, a ...any) {
		format = fmt.Sprintf("%s: %s", l.String(), format)
		t.Logf(format, a...)
	}
}
