package query_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db/query"
)

// Any error happening when beginning the transaction will be propagated.
func TestTransaction_BeginError(t *testing.T) {
	db := newDB(t)
	err := db.Close()
	require.NoError(t, err)

	err = query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error { return nil })
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Failed to begin transaction")
}

// Any error happening when in the transaction function will cause a rollback.
func TestTransaction_FunctionError(t *testing.T) {
	db := newDB(t)

	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE test (id INTEGER)")
		assert.NoError(t, err)
		return errors.New("boom")
	})
	assert.EqualError(t, err, "boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	tables, err := query.SelectStrings(context.Background(), tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "test")
}

// A transaction rolled back after cancellation must not report success.
func TestTransaction_CanceledBeforeCommit(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec("CREATE TABLE test (id INTEGER)")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = query.Transaction(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test VALUES (1)")
		require.NoError(t, err)
		cancel()

		// Wait for database/sql to roll back the canceled transaction.
		require.Eventually(t, func() bool {
			_, err := tx.Exec("SELECT 1")
			return errors.Is(err, sql.ErrTxDone)
		}, 5*time.Second, time.Millisecond)
		return nil
	})
	assert.ErrorIs(t, err, context.Canceled)

	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM test").Scan(&count))
	assert.Zero(t, count)
}

// Return a new in-memory SQLite database.
func newDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	return db
}
