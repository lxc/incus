package query_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

// Exercise possible failure modes.
func TestSelectObjects_Error(t *testing.T) {
	cases := []struct {
		dest  query.Dest
		query string
		error string
	}{
		{
			func(scan func(dest ...any) error) error {
				var row any
				return scan(row)
			},
			"SELECT id, name FROM test",
			"sql: expected 2 destination arguments in Scan, not 1",
		},
	}

	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			tx := newTxForObjects(t)

			stmt, err := tx.Prepare(c.query)
			require.NoError(t, err)

			err = query.SelectObjects(context.TODO(), stmt, c.dest)
			assert.EqualError(t, err, c.error)
		})
	}
}

// Scan rows yielded by the query.
func TestSelectObjects(t *testing.T) {
	tx := newTxForObjects(t)
	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	count := 0
	dest := func(scan func(dest ...any) error) error {
		require.Equal(t, 0, count, "expected at most one row to be yielded")
		count++

		return scan(&object.ID, &object.Name)
	}

	stmt, err := tx.Prepare("SELECT id, name FROM test WHERE name=?")
	require.NoError(t, err)

	err = query.SelectObjects(context.TODO(), stmt, dest, "bar")
	require.NoError(t, err)

	assert.Equal(t, 1, object.ID)
	assert.Equal(t, "bar", object.Name)
}

// Exercise possible failure modes.
func TestUpsertObject_Error(t *testing.T) {
	cases := []struct {
		columns []string
		values  []any
		error   string
	}{
		{
			[]string{},
			[]any{},
			"columns length is zero",
		},
		{
			[]string{"id"},
			[]any{2, "egg"},
			"columns length does not match values length",
		},
	}

	for _, c := range cases {
		t.Run(c.error, func(t *testing.T) {
			tx := newTxForObjects(t)
			id, err := query.UpsertObject(tx, "foo", c.columns, c.values)
			assert.Equal(t, int64(-1), id)
			assert.EqualError(t, err, c.error)
		})
	}
}

// Insert a new row.
func TestUpsertObject_Insert(t *testing.T) {
	tx := newTxForObjects(t)

	id, err := query.UpsertObject(tx, "test", []string{"name"}, []any{"egg"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), id)

	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	count := 0
	dest := func(scan func(dest ...any) error) error {
		require.Equal(t, 0, count, "expected at most one row to be yielded")
		count++

		return scan(&object.ID, &object.Name)
	}

	sql := "SELECT id, name FROM test WHERE name=?"
	err = query.Scan(context.TODO(), tx, sql, dest, "egg")
	require.NoError(t, err)

	assert.Equal(t, 2, object.ID)
	assert.Equal(t, "egg", object.Name)
}

// Update an existing row.
func TestUpsertObject_Update(t *testing.T) {
	tx := newTxForObjects(t)

	id, err := query.UpsertObject(tx, "test", []string{"id", "name"}, []any{1, "egg"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	objects := make([]struct {
		ID   int
		Name string
	}, 1)
	object := objects[0]

	count := 0
	dest := func(scan func(dest ...any) error) error {
		require.Equal(t, 0, count, "expected at most one row to be yielded")
		count++

		return scan(&object.ID, &object.Name)
	}

	sql := "SELECT id, name FROM test WHERE name=?"
	require.NoError(t, err)

	err = query.Scan(context.TODO(), tx, sql, dest, "egg")
	require.NoError(t, err)

	assert.Equal(t, 1, object.ID)
	assert.Equal(t, "egg", object.Name)
}

// Exercise possible failure modes.
func TestDeleteObject_Error(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "foo", 1)
	assert.False(t, deleted)
	assert.EqualError(t, err, "no such table: foo")
}

// If an row was actually deleted, the returned flag is true.
func TestDeleteObject_Deleted(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "test", 1)
	assert.True(t, deleted)
	assert.NoError(t, err)
}

// If no row was actually deleted, the returned flag is false.
func TestDeleteObject_NotDeleted(t *testing.T) {
	tx := newTxForObjects(t)

	deleted, err := query.DeleteObject(tx, "test", 1000)
	assert.False(t, deleted)
	assert.NoError(t, err)
}

// Return a new transaction against an in-memory SQLite database with a single
// test table populated with a few rows for testing object-related queries.
func newTxForObjects(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	assert.NoError(t, err)

	_, err = db.Exec("INSERT INTO test VALUES (0, 'foo'), (1, 'bar')")
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	return tx
}
