package node_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/db/node"
	"github.com/lxc/incus/internal/server/db/query"
)

func TestUpdateFromV38_RaftNodes(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(39, func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO raft_nodes VALUES (1, '1.2.3.4:666')")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	err = query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		roles, err := query.SelectIntegers(ctx, tx, "SELECT role FROM raft_nodes")
		require.NoError(t, err)
		assert.Equal(t, roles, []int{0})
		return nil
	})
	require.NoError(t, err)
}

func TestUpdateFromV36_RaftNodes(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(37, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO raft_nodes VALUES (1, '1.2.3.4:666')")
	require.NoError(t, err)
}

// All model tables previously in the node database have been migrated to the
// cluster database, and dropped from the node database.
func TestUpdateFromV36_DropTables(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(37, nil)
	require.NoError(t, err)

	var current []string
	err = query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stmt := "SELECT name FROM sqlite_master WHERE type='table'"
		current, err = query.SelectStrings(ctx, tx, stmt)
		return err
	})
	require.NoError(t, err)
	deleted := []string{
		"networks",
		"networks_config",
	}

	for _, name := range deleted {
		assert.False(t, slices.Contains(current, name))
	}
}

// If clustering is enabled, the core.https_address config gets copied to
// cluster.https_config.
func TestUpdateFromV37_CopyCoreHTTPSAddress(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(38, func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO raft_nodes VALUES (1, '1.2.3.4:666')")
		require.NoError(t, err)

		_, err = db.Exec("INSERT INTO config VALUES (1, 'core.https_address', '1.2.3.4:666')")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	var clusterAddress string
	err = query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		stmt := "SELECT value FROM config WHERE key='cluster.https_address'"
		row := tx.QueryRow(stmt)
		err := row.Scan(&clusterAddress)
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, clusterAddress, "1.2.3.4:666")
}

// If clustering is not enabled, the core.https_address config does not get copied.
func TestUpdateFromV37_NotClustered(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(38, func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO config VALUES (1, 'core.https_address', '1.2.3.4:666')")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	var clusterAddress string
	err = query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		stmt := "SELECT value FROM config WHERE key='cluster.https_address'"
		row := tx.QueryRow(stmt)
		err := row.Scan(&clusterAddress)
		return err
	})
	require.EqualError(t, err, "sql: no rows in result set")
}
