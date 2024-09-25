package query

import (
	"context"
	"database/sql"
	"fmt"
)

// Count returns the number of rows in the given table.
func Count(ctx context.Context, tx *sql.Tx, table string, where string, args ...any) (int, error) {
	stmt := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if where != "" {
		stmt += fmt.Sprintf(" WHERE %s", where)
	}

	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return -1, err
	}

	defer func() { _ = rows.Close() }()

	// Ensure we read one and only one row.
	if !rows.Next() {
		return -1, fmt.Errorf("no rows returned")
	}

	var count int
	err = rows.Scan(&count)
	if err != nil {
		return -1, fmt.Errorf("failed to scan count column")
	}

	if rows.Next() {
		return -1, fmt.Errorf("more than one row returned")
	}

	err = rows.Err()
	if err != nil {
		return -1, err
	}

	return count, nil
}

// CountAll returns a map associating each table name in the database
// with the total count of its rows.
func CountAll(ctx context.Context, tx *sql.Tx) (map[string]int, error) {
	tables, err := SelectStrings(ctx, tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch table names: %w", err)
	}

	counts := map[string]int{}
	for _, table := range tables {
		count, err := Count(ctx, tx, table, "")
		if err != nil {
			return nil, fmt.Errorf("Failed to count rows of %s: %w", table, err)
		}

		counts[table] = count
	}

	return counts, nil
}
