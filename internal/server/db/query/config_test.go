package query_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

func TestSelectConfig(t *testing.T) {
	tx := newTxForConfig(t)
	values, err := query.SelectConfig(context.Background(), tx, "test", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "x", "bar": "zz"}, values)
}

func TestSelectConfig_WithFilters(t *testing.T) {
	tx := newTxForConfig(t)
	values, err := query.SelectConfig(context.Background(), tx, "test", "key=?", "bar")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"bar": "zz"}, values)
}

// New keys are added to the table.
func TestUpdateConfig_NewKeys(t *testing.T) {
	tx := newTxForConfig(t)

	values := map[string]string{"foo": "y"}
	err := query.UpdateConfig(tx, "test", values)
	require.NoError(t, err)

	values, err = query.SelectConfig(context.Background(), tx, "test", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"foo": "y", "bar": "zz"}, values)
}

// Unset keys are deleted from the table.
func TestDeleteConfig_Delete(t *testing.T) {
	tx := newTxForConfig(t)
	values := map[string]string{"foo": ""}

	err := query.UpdateConfig(tx, "test", values)

	require.NoError(t, err)
	values, err = query.SelectConfig(context.Background(), tx, "test", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"bar": "zz"}, values)
}

// Return a new transaction against an in-memory SQLite database with a single
// test table populated with a few rows.
func newTxForConfig(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (key TEXT NOT NULL, value TEXT)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES ('foo', 'x'), ('bar', 'zz')")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
