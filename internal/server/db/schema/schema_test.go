package schema_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/server/db/schema"
	"github.com/lxc/incus/v6/shared/util"
)

// WriteTempFile creates a temp file with the specified content.
func WriteTempFile(dir string, prefix string, content string) (string, error) {
	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return "", err
	}

	defer func() { _ = f.Close() }()

	_, err = f.WriteString(content)
	if err != nil {
		return "", err
	}

	return f.Name(), f.Close()
}

// Create a new Schema by specifying an explicit map from versions to Update
// functions.
func TestNewFromMap(t *testing.T) {
	db := newDB(t)
	schema := schema.NewFromMap(map[int]schema.Update{
		1: updateCreateTable,
		2: updateInsertValue,
	})
	initial, err := schema.Ensure(db)
	assert.NoError(t, err)
	assert.Equal(t, 0, initial)
}

// Panic if there are missing versions in the map.
func TestNewFromMap_MissingVersions(t *testing.T) {
	assert.Panics(t, func() {
		schema.NewFromMap(map[int]schema.Update{
			1: updateCreateTable,
			3: updateInsertValue,
		})
	}, "updates map misses version 2")
}

// If the database schema version is more recent than our update series, an
// error is returned.
func TestSchemaEnsure_VersionMoreRecentThanExpected(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateNoop)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	schema, _ = newSchemaAndDB(t)
	_, err = schema.Ensure(db)
	assert.NotNil(t, err)
	assert.EqualError(t, err, "schema version '1' is more recent than expected '0'")
}

// If a "fresh" SQL statement for creating the schema from scratch is provided,
// but it fails to run, an error is returned.
func TestSchemaEnsure_FreshStatementError(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateNoop)
	schema.Fresh("garbage")

	_, err := schema.Ensure(db)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot apply fresh schema")
}

// If the database schema contains "holes" in the applied versions, an error is
// returned.
func TestSchemaEnsure_MissingVersion(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateNoop)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	_, err = db.Exec(`INSERT INTO schema (version, updated_at) VALUES (3, strftime("%s"))`)
	assert.NoError(t, err)

	schema.Add(updateNoop)
	schema.Add(updateNoop)

	_, err = schema.Ensure(db)
	assert.NotNil(t, err)
	assert.EqualError(t, err, "Missing updates: 1 to 3")
}

// If the schema has no update, the schema table gets created and has no version.
func TestSchemaEnsure_ZeroUpdates(t *testing.T) {
	schema, db := newSchemaAndDB(t)

	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{}, versions)
}

// If the schema has updates and no one was applied yet, all of them get
// applied.
func TestSchemaEnsure_ApplyAllUpdates(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateInsertValue)

	initial, err := schema.Ensure(db)
	assert.NoError(t, err)
	assert.Equal(t, 0, initial)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// THe update version is recorded.
	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versions)

	// The two updates have been applied in order.
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT id FROM test")
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, ids)
}

// If the schema schema has been created using a dump, the schema table will
// contain just one row with the update level associated with the dump. It's
// possible to apply further updates from there, and only these new ones will
// be inserted in the schema table.
func TestSchemaEnsure_ApplyAfterInitialDumpCreation(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateAddColumn)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	dump, err := schema.Dump(db)
	assert.NoError(t, err)

	_, db = newSchemaAndDB(t)
	schema.Fresh(dump)
	_, err = schema.Ensure(db)
	assert.NoError(t, err)

	schema.Add(updateNoop)
	_, err = schema.Ensure(db)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Only updates starting from the initial dump are recorded.
	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 3}, versions)
}

// If the schema has updates and part of them were already applied, only the
// missing ones are applied.
func TestSchemaEnsure_OnlyApplyMissing(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	schema.Add(updateInsertValue)
	initial, err := schema.Ensure(db)
	assert.NoError(t, err)
	assert.Equal(t, 1, initial)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// All update versions are recorded.
	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM SCHEMA")
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versions)

	// The two updates have been applied in order.
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT id FROM test")
	assert.NoError(t, err)
	assert.Equal(t, []int{1}, ids)
}

// If a update fails, an error is returned, and all previous changes are rolled
// back.
func TestSchemaEnsure_FailingUpdate(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateBoom)
	_, err := schema.Ensure(db)
	assert.EqualError(t, err, "failed to apply update 1: boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Not update was applied.
	tables, err := query.SelectStrings(context.Background(), tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "schema")
	assert.NotContains(t, tables, "test")
}

// If a hook fails, an error is returned, and all previous changes are rolled
// back.
func TestSchemaEnsure_FailingHook(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Hook(func(context.Context, int, *sql.Tx) error { return fmt.Errorf("boom") })
	_, err := schema.Ensure(db)
	assert.EqualError(t, err, "failed to execute hook (version 0): boom")

	tx, err := db.Begin()
	assert.NoError(t, err)

	// Not update was applied.
	tables, err := query.SelectStrings(context.Background(), tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	assert.NoError(t, err)
	assert.NotContains(t, tables, "schema")
	assert.NotContains(t, tables, "test")
}

// If the schema check callback returns ErrGracefulAbort, the process is
// aborted, although every change performed so far gets still committed.
func TestSchemaEnsure_CheckGracefulAbort(t *testing.T) {
	check := func(ctx context.Context, current int, tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE test (n INTEGER)")
		require.NoError(t, err)
		return schema.ErrGracefulAbort
	}

	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Check(check)

	_, err := schema.Ensure(db)
	require.EqualError(t, err, "schema check gracefully aborted")

	tx, err := db.Begin()
	assert.NoError(t, err)

	// The table created by the check function still got committed.
	// to insert the row was not.
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT n FROM test")
	assert.NoError(t, err)
	assert.Equal(t, []int{}, ids)
}

// The SQL text returns by Dump() can be used to create the schema from
// scratch, without applying each individual update.
func TestSchemaDump(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.Add(updateAddColumn)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)

	dump, err := schema.Dump(db)
	assert.NoError(t, err)

	_, db = newSchemaAndDB(t)
	schema.Fresh(dump)
	_, err = schema.Ensure(db)
	assert.NoError(t, err)

	tx, err := db.Begin()
	assert.NoError(t, err)

	// All update versions are in place.
	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM schema")
	assert.NoError(t, err)
	assert.Equal(t, []int{2}, versions)

	// Both the table added by the first update and the extra column added
	// by the second update are there.
	_, err = tx.Exec("SELECT id, name FROM test")
	assert.NoError(t, err)
}

// If not all updates are applied, Dump() returns an error.
func TestSchemaDump_MissingUpdatees(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	_, err := schema.Ensure(db)
	assert.NoError(t, err)
	schema.Add(updateAddColumn)

	_, err = schema.Dump(db)
	assert.EqualError(t, err, "update level is 1, expected 2")
}

// After trimming a schema, only the updates up to the trim point are applied.
func TestSchema_Trim(t *testing.T) {
	updates := map[int]schema.Update{
		1: updateCreateTable,
		2: updateInsertValue,
		3: updateAddColumn,
	}

	schema := schema.NewFromMap(updates)
	trimmed := schema.Trim(2)
	assert.Len(t, trimmed, 1)

	db := newDB(t)
	_, err := schema.Ensure(db)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	versions, err := query.SelectIntegers(context.Background(), tx, "SELECT version FROM schema")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, versions)
}

// Exercise a given update in a schema.
func TestSchema_ExeciseUpdate(t *testing.T) {
	updates := map[int]schema.Update{
		1: updateCreateTable,
		2: updateInsertValue,
		3: updateAddColumn,
	}

	schema := schema.NewFromMap(updates)
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	// Update 2 has been applied.
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT id FROM test")
	require.NoError(t, err)
	assert.Equal(t, []int{1}, ids)

	// Update 3 has not been applied.
	_, err = query.SelectStrings(context.Background(), tx, "SELECT name FROM test")
	require.EqualError(t, err, "no such column: name")
}

// A custom schema file path is given, but it does not exists. This is a no-op.
func TestSchema_File_NotExists(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)
	schema.File("/non/existing/file/path")

	_, err := schema.Ensure(db)
	require.NoError(t, err)
}

// A custom schema file path is given, but it contains non valid SQL. An error
// is returned an no change to the database is performed at all.
func TestSchema_File_Garbage(t *testing.T) {
	schema, db := newSchemaAndDB(t)
	schema.Add(updateCreateTable)

	path, err := WriteTempFile("", "incus-db-schema-", "SELECT FROM baz")
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	schema.File(path)

	_, err = schema.Ensure(db)

	message := fmt.Sprintf("failed to execute queries from %s: near \"FROM\": syntax error", path)
	require.EqualError(t, err, message)
}

// A custom schema file path is given, it runs some queries that repair an
// otherwise broken update, before the update is run.
func TestSchema_File(t *testing.T) {
	schema, db := newSchemaAndDB(t)

	// Add an update that would insert a value into a non-existing table.
	schema.Add(updateInsertValue)

	path, err := WriteTempFile("", "incus-db-schema-",
		`CREATE TABLE test (id INTEGER);
INSERT INTO test VALUES (2);
`)
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	schema.File(path)

	_, err = schema.Ensure(db)
	require.NoError(t, err)

	// The file does not exist anymore.
	assert.False(t, util.PathExists(path))

	// The table was created, and the extra row inserted as well.
	tx, err := db.Begin()
	require.NoError(t, err)

	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT id FROM test ORDER BY id")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, ids)
}

// A both a custom schema file path and a hook are set, the hook runs before
// the queries in the file are executed.
func TestSchema_File_Hook(t *testing.T) {
	schema, db := newSchemaAndDB(t)

	// Add an update that would insert a value into a non-existing table.
	schema.Add(updateInsertValue)

	// Add a custom schema update query file that inserts a value into a
	// non-existing table.
	path, err := WriteTempFile("", "incus-db-schema-", "INSERT INTO test VALUES (2)")
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	schema.File(path)

	// Add a hook that takes care of creating the test table, this shows
	// that it's run before anything else.
	schema.Hook(func(ctx context.Context, version int, tx *sql.Tx) error {
		if version == -1 {
			_, err := tx.Exec("CREATE TABLE test (id INTEGER)")
			return err
		}

		return nil
	})

	_, err = schema.Ensure(db)
	require.NoError(t, err)

	// The table was created, and the both rows inserted as well.
	tx, err := db.Begin()
	require.NoError(t, err)

	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT id FROM test ORDER BY id")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, ids)
}

// Return a new in-memory SQLite database.
func newDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	return db
}

// Return both an empty schema and a test database.
func newSchemaAndDB(t *testing.T) (*schema.Schema, *sql.DB) {
	return schema.Empty(), newDB(t)
}

// An update that does nothing.
func updateNoop(context.Context, *sql.Tx) error {
	return nil
}

// An update that creates a test table.
func updateCreateTable(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("CREATE TABLE test (id INTEGER)")
	return err
}

// An update that inserts a value into the test table.
func updateInsertValue(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("INSERT INTO test VALUES (1)")
	return err
}

// An update that adds a column to the test tabble.
func updateAddColumn(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE test ADD COLUMN name TEXT")
	return err
}

// An update that unconditionally fails with an error.
func updateBoom(ctx context.Context, tx *sql.Tx) error {
	return fmt.Errorf("boom")
}
