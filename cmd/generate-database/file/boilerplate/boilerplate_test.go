package boilerplate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func Test(t *testing.T) {
	// Fake the usage of the private variables and functions in the boilerplate.
	_ = mapErr
	_ = defaultMapErr
	_ = marshal
	_ = unmarshal
	_ = marshalJSON
	_ = unmarshalJSON
	_ = selectObjects
	_ = scan
}

func openTestDB(t *testing.T, name string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), name+".db"))
	require.NoError(t, err)

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func prepareTestStmt(t *testing.T, db *sql.DB, value string) map[int]*sql.Stmt {
	t.Helper()

	stmt, err := db.Prepare("SELECT '" + value + "'")
	require.NoError(t, err)

	t.Cleanup(func() { _ = stmt.Close() })

	return map[int]*sql.Stmt{0: stmt}
}

func TestStmtContext(t *testing.T) {
	globalDB := openTestDB(t, "global")
	contextDB := openTestDB(t, "context")

	globalStmts := prepareTestStmt(t, globalDB, "global")
	contextStmts := prepareTestStmt(t, contextDB, "context")

	// PreparedStmts is global. Set it before the parallel subtests start.
	oldPreparedStmts := PreparedStmts
	PreparedStmts = globalStmts
	t.Cleanup(func() { PreparedStmts = oldPreparedStmts })

	tests := []struct {
		name  string
		ctx   context.Context
		dbtx  func(t *testing.T) dbtx
		value string
	}{
		{
			name: "global prepared statements without context value",
			ctx:  context.Background(),
			dbtx: func(t *testing.T) dbtx {
				return globalDB
			},
			value: "global",
		},
		{
			name: "context prepared statements take precedence",
			ctx:  WithPreparedStmts(context.Background(), contextStmts),
			dbtx: func(t *testing.T) dbtx {
				return contextDB
			},
			value: "context",
		},
		{
			name: "context prepared statements in transaction",
			ctx:  WithPreparedStmts(context.Background(), contextStmts),
			dbtx: func(t *testing.T) dbtx {
				tx, err := contextDB.Begin()
				require.NoError(t, err)

				t.Cleanup(func() { _ = tx.Rollback() })

				return tx
			},
			value: "context",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			db := tc.dbtx(t)

			// Run test
			stmt, err := StmtContext(tc.ctx, db, 0)
			require.NoError(t, err)

			var value string
			err = stmt.QueryRowContext(tc.ctx).Scan(&value)

			// Assert
			require.NoError(t, err, "Query with the returned statement must succeed")
			require.Equal(t, tc.value, value, "Statement must come from the expected set of prepared statements")
		})
	}
}

func TestStmtContextNotRegistered(t *testing.T) {
	t.Parallel()

	ctx := WithPreparedStmts(context.Background(), map[int]*sql.Stmt{})

	_, err := StmtContext(ctx, openTestDB(t, "empty"), 0)

	require.ErrorContains(t, err, "No prepared statement registered with code 0")
}
