package schema

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lxc/incus/internal/server/db/query"
)

// Schema captures the schema of a database in terms of a series of ordered
// updates.
type Schema struct {
	updates []Update // Ordered series of updates making up the schema
	hook    Hook     // Optional hook to execute whenever a update gets applied
	fresh   string   // Optional SQL statement used to create schema from scratch
	check   Check    // Optional callback invoked before doing any update
	path    string   // Optional path to a file containing extra queries to run
}

// Update applies a specific schema change to a database, and returns an error
// if anything goes wrong.
type Update func(context.Context, *sql.Tx) error

// Hook is a callback that gets fired when a update gets applied.
type Hook func(context.Context, int, *sql.Tx) error

// Check is a callback that gets fired all the times Schema.Ensure is invoked,
// before applying any update. It gets passed the version that the schema is
// currently at and a handle to the transaction. If it returns nil, the update
// proceeds normally, otherwise it's aborted. If ErrGracefulAbort is returned,
// the transaction will still be committed, giving chance to this function to
// perform state changes.
type Check func(context.Context, int, *sql.Tx) error

// New creates a new schema Schema with the given updates.
func New(updates []Update) *Schema {
	return &Schema{
		updates: updates,
	}
}

// NewFromMap creates a new schema Schema with the updates specified in the
// given map. The keys of the map are schema versions that when upgraded will
// trigger the associated Update value. It's required that the minimum key in
// the map is 1, and if key N is present then N-1 is present too, with N>1
// (i.e. there are no missing versions).
//
// NOTE: the regular New() constructor would be formally enough, but for extra
// clarity we also support a map that indicates the version explicitly,
// see also PR #3704.
func NewFromMap(versionsToUpdates map[int]Update) *Schema {
	// Collect all version keys.
	versions := []int{}
	for version := range versionsToUpdates {
		versions = append(versions, version)
	}

	// Sort the versions,
	sort.Ints(versions)

	// Build the updates slice.
	updates := []Update{}
	for i, version := range versions {
		// Assert that we start from 1 and there are no gaps.
		if version != i+1 {
			panic(fmt.Sprintf("updates map misses version %d", i+1))
		}

		updates = append(updates, versionsToUpdates[version])
	}

	return &Schema{
		updates: updates,
	}
}

// Empty creates a new schema with no updates.
func Empty() *Schema {
	return New([]Update{})
}

// Add a new update to the schema. It will be appended at the end of the
// existing series.
func (s *Schema) Add(update Update) {
	s.updates = append(s.updates, update)
}

// Hook instructs the schema to invoke the given function whenever a update is
// about to be applied. The function gets passed the update version number and
// the running transaction, and if it returns an error it will cause the schema
// transaction to be rolled back. Any previously installed hook will be
// replaced.
func (s *Schema) Hook(hook Hook) {
	s.hook = hook
}

// Check instructs the schema to invoke the given function whenever Ensure is
// invoked, before applying any due update. It can be used for aborting the
// operation.
func (s *Schema) Check(check Check) {
	s.check = check
}

// Fresh sets a statement that will be used to create the schema from scratch
// when bootstraping an empty database. It should be a "flattening" of the
// available updates, generated using the Dump() method. If not given, all
// patches will be applied in order.
func (s *Schema) Fresh(statement string) {
	s.fresh = statement
}

// File extra queries from a file. If the file is exists, all SQL queries in it
// will be executed transactionally at the very start of Ensure(), before
// anything else is done.
//
// If a schema hook was set with Hook(), it will be run before running the
// queries in the file and it will be passed a patch version equals to -1.
func (s *Schema) File(path string) {
	s.path = path
}

// Ensure makes sure that the actual schema in the given database matches the
// one defined by our updates.
//
// All updates are applied transactionally. In case any error occurs the
// transaction will be rolled back and the database will remain unchanged.
//
// A update will be applied only if it hasn't been before (currently applied
// updates are tracked in the a 'shema' table, which gets automatically
// created).
//
// If no error occurs, the integer returned by this method is the
// initial version that the schema has been upgraded from.
func (s *Schema) Ensure(db *sql.DB) (int, error) {
	var current int
	aborted := false
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		err := execFromFile(ctx, tx, s.path, s.hook)
		if err != nil {
			return fmt.Errorf("failed to execute queries from %s: %w", s.path, err)
		}

		err = ensureSchemaTableExists(ctx, tx)
		if err != nil {
			return err
		}

		current, err = queryCurrentVersion(ctx, tx)
		if err != nil {
			return err
		}

		if s.check != nil {
			err := s.check(ctx, current, tx)
			if err == ErrGracefulAbort {
				// Abort the update gracefully, committing what
				// we've done so far.
				aborted = true
				return nil
			}

			if err != nil {
				return err
			}
		}

		// When creating the schema from scratch, use the fresh dump if
		// available. Otherwise just apply all relevant updates.
		if current == 0 && s.fresh != "" {
			_, err = tx.Exec(s.fresh)
			if err != nil {
				return fmt.Errorf("cannot apply fresh schema: %w", err)
			}
		} else {
			err = ensureUpdatesAreApplied(ctx, tx, current, s.updates, s.hook)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return -1, err
	}

	if aborted {
		return current, ErrGracefulAbort
	}

	return current, nil
}

// Dump returns a text of SQL commands that can be used to create this schema
// from scratch in one go, without going thorugh individual patches
// (essentially flattening them).
//
// It requires that all patches in this schema have been applied, otherwise an
// error will be returned.
func (s *Schema) Dump(db *sql.DB) (string, error) {
	var statements []string
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		err := checkAllUpdatesAreApplied(ctx, tx, s.updates)
		if err != nil {
			return err
		}

		statements, err = selectTablesSQL(ctx, tx)
		return err
	})
	if err != nil {
		return "", err
	}

	for i, statement := range statements {
		statements[i] = formatSQL(statement)
	}

	// Add a statement for inserting the current schema version row.
	statements = append(
		statements,
		fmt.Sprintf(`
INSERT INTO schema (version, updated_at) VALUES (%d, strftime("%%s"))
`, len(s.updates)))
	return strings.Join(statements, ";\n"), nil
}

// Trim the schema updates to the given version (included). Updates with higher
// versions will be discarded. Any fresh schema dump previously set will be
// unset, since it's assumed to no longer be applicable. Return all updates
// that have been trimmed.
func (s *Schema) Trim(version int) []Update {
	trimmed := s.updates[version:]
	s.updates = s.updates[:version]
	s.fresh = ""
	return trimmed
}

// ExerciseUpdate is a convenience for exercising a particular update of a
// schema.
//
// It first creates an in-memory SQLite database, then it applies all updates
// up to the one with given version (excluded) and optionally executes the
// given hook for populating the database with test data. Finally it applies
// the update with the given version, returning the database handle for further
// inspection of the resulting state.
func (s *Schema) ExerciseUpdate(version int, hook func(*sql.DB)) (*sql.DB, error) {
	// Create an in-memory database.
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("failed to open memory database: %w", err)
	}

	// Apply all updates to the given version, excluded.
	trimmed := s.Trim(version - 1)
	_, err = s.Ensure(db)
	if err != nil {
		return nil, fmt.Errorf("failed to apply previous updates: %w", err)
	}

	// Execute the optional hook.
	if hook != nil {
		hook(db)
	}

	// Apply the update with the given version
	s.Add(trimmed[0])
	_, err = s.Ensure(db)
	if err != nil {
		return nil, fmt.Errorf("failed to apply given update: %w", err)
	}

	return db, nil
}

// Ensure that the schema exists.
func ensureSchemaTableExists(ctx context.Context, tx *sql.Tx) error {
	exists, err := DoesSchemaTableExist(ctx, tx)
	if err != nil {
		return fmt.Errorf("failed to check if schema table is there: %w", err)
	}

	if !exists {
		err := createSchemaTable(tx)
		if err != nil {
			return fmt.Errorf("failed to create schema table: %w", err)
		}
	}
	return nil
}

// Return the highest update version currently applied. Zero means that no
// updates have been applied yet.
func queryCurrentVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	versions, err := selectSchemaVersions(ctx, tx)
	if err != nil {
		return -1, fmt.Errorf("failed to fetch update versions: %w", err)
	}

	// Fix bad upgrade code between 30 and 32
	hasVersion := func(v int) bool { return slices.Contains(versions, v) }
	if hasVersion(30) && hasVersion(32) && !hasVersion(31) {
		err = insertSchemaVersion(tx, 31)
		if err != nil {
			return -1, fmt.Errorf("failed to insert missing schema version 31")
		}

		versions, err = selectSchemaVersions(ctx, tx)
		if err != nil {
			return -1, fmt.Errorf("failed to fetch update versions: %w", err)
		}
	}

	// Fix broken schema version between 37 and 38
	if hasVersion(37) && !hasVersion(38) {
		count, err := query.Count(ctx, tx, "config", "key = 'cluster.https_address'")
		if err != nil {
			return -1, fmt.Errorf("Failed to check if cluster.https_address is set: %w", err)
		}

		if count == 1 {
			// Insert the missing version.
			err := insertSchemaVersion(tx, 38)
			if err != nil {
				return -1, fmt.Errorf("Failed to insert missing schema version 38")
			}

			versions = append(versions, 38)
		}
	}

	current := 0
	if len(versions) > 0 {
		err = checkSchemaVersionsHaveNoHoles(versions)
		if err != nil {
			return -1, err
		}

		current = versions[len(versions)-1] // Highest recorded version
	}

	return current, nil
}

// Apply any pending update that was not yet applied.
func ensureUpdatesAreApplied(ctx context.Context, tx *sql.Tx, current int, updates []Update, hook Hook) error {
	if current > len(updates) {
		return fmt.Errorf(
			"schema version '%d' is more recent than expected '%d'",
			current, len(updates))
	}

	// If there are no updates, there's nothing to do.
	if len(updates) == 0 {
		return nil
	}

	// Apply missing updates.
	for _, update := range updates[current:] {
		if hook != nil {
			err := hook(ctx, current, tx)
			if err != nil {
				return fmt.Errorf(
					"failed to execute hook (version %d): %v", current, err)
			}
		}
		err := update(ctx, tx)
		if err != nil {
			return fmt.Errorf("failed to apply update %d: %w", current, err)
		}

		current++

		err = insertSchemaVersion(tx, current)
		if err != nil {
			return fmt.Errorf("failed to insert version %d", current)
		}
	}

	return nil
}

// Check that the given list of update version numbers doesn't have "holes",
// that is each version equal the preceding version plus 1.
func checkSchemaVersionsHaveNoHoles(versions []int) error {
	// Ensure that there are no "holes" in the recorded versions.
	for i := range versions[:len(versions)-1] {
		if versions[i+1] != versions[i]+1 {
			return fmt.Errorf("Missing updates: %d to %d", versions[i], versions[i+1])
		}
	}
	return nil
}

// Check that all the given updates are applied.
func checkAllUpdatesAreApplied(ctx context.Context, tx *sql.Tx, updates []Update) error {
	versions, err := selectSchemaVersions(ctx, tx)
	if err != nil {
		return fmt.Errorf("failed to fetch update versions: %w", err)
	}

	if len(versions) == 0 {
		return fmt.Errorf("expected schema table to contain at least one row")
	}

	err = checkSchemaVersionsHaveNoHoles(versions)
	if err != nil {
		return err
	}

	current := versions[len(versions)-1]
	if current != len(updates) {
		return fmt.Errorf("update level is %d, expected %d", current, len(updates))
	}

	return nil
}

// Format the given SQL statement in a human-readable way.
//
// In particular make sure that each column definition in a CREATE TABLE clause
// is in its own row, since SQLite dumps occasionally stuff more than one
// column in the same line.
func formatSQL(statement string) string {
	lines := strings.Split(statement, "\n")
	for i, line := range lines {
		if strings.Contains(line, "UNIQUE") {
			// Let UNIQUE(x, y) constraints alone.
			continue
		}

		lines[i] = strings.Replace(line, ", ", ",\n    ", -1)
	}

	return strings.Join(lines, "\n")
}
