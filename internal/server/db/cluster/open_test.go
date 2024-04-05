package cluster_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/osarch"
)

// If the node is not clustered, the schema updates works normally.
func TestEnsureSchema_NoClustered(t *testing.T) {
	dir, cleanup := newDir(t)
	defer cleanup()
	assert.NoError(t, os.Mkdir(filepath.Join(dir, "global"), 0711))
	db := newDB(t)
	addNode(t, db, "0.0.0.0", 1, 1)
	ready, err := cluster.EnsureSchema(db, "1.2.3.4:666", dir)
	assert.True(t, ready)
	assert.NoError(t, err)
}

// Exercise EnsureSchema failures when the cluster can't be upgraded right now.
func TestEnsureSchema_ClusterNotUpgradable(t *testing.T) {
	schema := cluster.SchemaVersion
	apiExtensions := len(version.APIExtensions)

	cases := []struct {
		title string
		setup func(*testing.T, *sql.DB)
		ready bool
		error string
	}{
		{
			`a node's schema version is behind`,
			func(t *testing.T, db *sql.DB) {
				addNode(t, db, "1", schema, apiExtensions)
				addNode(t, db, "2", schema-1, apiExtensions)
			},
			false, // The schema was not updated
			"",    // No error is returned
		},
		{
			`a node's number of API extensions is behind`,
			func(t *testing.T, db *sql.DB) {
				addNode(t, db, "1", schema, apiExtensions)
				addNode(t, db, "2", schema, apiExtensions-1)
			},
			false, // The schema was not updated
			"",    // No error is returned
		},
		{
			`this node's schema is behind`,
			func(t *testing.T, db *sql.DB) {
				addNode(t, db, "1", schema, apiExtensions)
				addNode(t, db, "2", schema+1, apiExtensions)
			},
			false,
			"this node's version is behind, please upgrade",
		},
		{
			`this node's number of API extensions is behind`,
			func(t *testing.T, db *sql.DB) {
				addNode(t, db, "1", schema, apiExtensions)
				addNode(t, db, "2", schema, apiExtensions+1)
			},
			false,
			"this node's version is behind, please upgrade",
		},
		{
			`inconsistent schema version and API extensions number`,
			func(t *testing.T, db *sql.DB) {
				addNode(t, db, "1", schema, apiExtensions)
				addNode(t, db, "2", schema+1, apiExtensions-1)
			},
			false,
			"Cluster members have inconsistent versions",
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			db := newDB(t)
			c.setup(t, db)
			ready, err := cluster.EnsureSchema(db, "1", "/unused/db/dir")
			assert.Equal(t, c.ready, ready)
			if c.error == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, c.error)
			}
		})
	}
}

// Regardless of whether the schema could actually be upgraded or not, the
// version of this node gets updated.
func TestEnsureSchema_UpdateNodeVersion(t *testing.T) {
	schema := cluster.SchemaVersion
	apiExtensions := len(version.APIExtensions)

	cases := []struct {
		setup func(*testing.T, *sql.DB)
		ready bool
	}{
		{
			func(t *testing.T, db *sql.DB) {},
			true,
		},
		{
			func(t *testing.T, db *sql.DB) {
				// Add a node which is behind.
				addNode(t, db, "2", schema, apiExtensions-1)
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%v", c.ready), func(t *testing.T) {
			db := newDB(t)

			// Add ourselves with an older schema version and API
			// extensions number.
			addNode(t, db, "1", schema-1, apiExtensions-1)

			// Ensure the schema.
			ready, err := cluster.EnsureSchema(db, "1", "/unused/db/dir")
			assert.NoError(t, err)
			assert.Equal(t, c.ready, ready)

			// Check that the nodes table was updated with our new
			// schema version and API extensions number.
			assertNode(t, db, "1", schema, apiExtensions)
		})
	}
}

// Create a new in-memory SQLite database with a fresh cluster schema.
func newDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	createTableSchema := `
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
`
	_, err = db.Exec(createTableSchema + cluster.FreshSchema())
	require.NoError(t, err)

	return db
}

// Add a new node with the given address, schema version and number of api extensions.
func addNode(t *testing.T, db *sql.DB, address string, schema int, apiExtensions int) {
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		stmt := `
INSERT INTO nodes(name, address, schema, api_extensions, arch, description) VALUES (?, ?, ?, ?, ?, '')
`
		name := fmt.Sprintf("node at %s", address)
		_, err := tx.Exec(stmt, name, address, schema, apiExtensions, osarch.ARCH_64BIT_INTEL_X86)
		return err
	})
	require.NoError(t, err)
}

// Assert that the node with the given address has the given schema version and API
// extensions number.
func assertNode(t *testing.T, db *sql.DB, address string, schema int, apiExtensions int) {
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		where := "address=? AND schema=? AND api_extensions=?"
		n, err := query.Count(ctx, tx, "nodes", where, address, schema, apiExtensions)
		assert.Equal(t, 1, n, "node does not have expected version")
		return err
	})
	require.NoError(t, err)
}

// Return a new temporary directory.
func newDir(t *testing.T) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "dqlite-replication-test-")
	assert.NoError(t, err)

	cleanup := func() {
		_, err := os.Stat(dir)
		if err != nil {
			assert.True(t, os.IsNotExist(err))
		} else {
			assert.NoError(t, os.RemoveAll(dir))
		}
	}

	return dir, cleanup
}
