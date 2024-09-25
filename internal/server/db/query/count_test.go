package query_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

// Count returns the current number of rows.
func TestCount(t *testing.T) {
	cases := []struct {
		where string
		args  []any
		count int
	}{
		{
			"id=?",
			[]any{999},
			0,
		},
		{
			"id=?",
			[]any{1},
			1,
		},
		{
			"",
			[]any{},
			2,
		},
	}

	for _, c := range cases {
		t.Run(strconv.Itoa(c.count), func(t *testing.T) {
			tx := newTxForCount(t)
			count, err := query.Count(context.Background(), tx, "test", c.where, c.args...)
			require.NoError(t, err)
			assert.Equal(t, c.count, count)
		})
	}
}

func TestCountAll(t *testing.T) {
	tx := newTxForCount(t)
	defer func() { _ = tx.Rollback() }()

	counts, err := query.CountAll(context.Background(), tx)
	require.NoError(t, err)

	assert.Equal(t, map[string]int{
		"test":  2,
		"test2": 1,
	}, counts)
}

// Return a new transaction against an in-memory SQLite database with a single
// test table and a few rows.
func newTxForCount(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (id INTEGER)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES (1), (2)")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test2 (id INTEGER)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test2 VALUES (1)")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
