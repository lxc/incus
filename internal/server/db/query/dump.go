package query

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dump returns a SQL text dump of all rows across all tables, similar to
// sqlite3's dump feature.
func Dump(ctx context.Context, tx *sql.Tx, schemaOnly bool) (string, error) {
	entitiesSchemas, entityNames, err := getEntitiesSchemas(ctx, tx)
	if err != nil {
		return "", err
	}

	// Begin dump string.
	var builder strings.Builder
	builder.WriteString("PRAGMA foreign_keys=OFF;\n")
	builder.WriteString("BEGIN TRANSACTION;\n")

	// For each table, write the schema and optionally write the data.
	for _, tableName := range entityNames {
		builder.WriteString(entitiesSchemas[tableName][1] + "\n")

		if !schemaOnly && entitiesSchemas[tableName][0] == "table" {
			tableData, err := getTableData(ctx, tx, tableName)
			if err != nil {
				return "", err
			}

			for _, stmt := range tableData {
				builder.WriteString(stmt + "\n")
			}
		}
	}

	// Sequences (unless the schemaOnly flag is true).
	if !schemaOnly {
		builder.WriteString("DELETE FROM sqlite_sequence;\n")

		tableData, err := getTableData(ctx, tx, "sqlite_sequence")
		if err != nil {
			return "", fmt.Errorf("Failed to dump table sqlite_sequence: %w", err)
		}

		for _, stmt := range tableData {
			builder.WriteString(stmt + "\n")
		}
	}

	// Commit.
	builder.WriteString("COMMIT;\n")

	return builder.String(), nil
}

// getEntitiesSchemas gets all the tables, their kind, and their schema, as well as a list of entity names in their default order from
// the sqlite_master table. The returned map values are arrays of length 2 whose first element contains the entity type and the second
// contains it's schema.
func getEntitiesSchemas(ctx context.Context, tx *sql.Tx) (map[string][2]string, []string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name, type, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY rowid`)
	if err != nil {
		return nil, nil, fmt.Errorf("Could not get table names and their schema: %w", err)
	}

	defer func() { _ = rows.Close() }()

	tablesSchemas := make(map[string][2]string)
	var names []string
	for rows.Next() {
		var name string
		var kind string
		var schema string
		err := rows.Scan(&name, &kind, &schema)
		if err != nil {
			return nil, nil, fmt.Errorf("Could not scan table name and schema: %w", err)
		}

		// This is based on logic from dump_callback in sqlite source for sqlite3_db_dump function.
		if strings.HasPrefix(schema, `CREATE TABLE "`) {
			schema = strings.Replace(schema, "CREATE TABLE", "CREATE TABLE IF NOT EXISTS", 1)
		}

		names = append(names, name)
		tablesSchemas[name] = [2]string{kind, schema + ";"}
	}

	return tablesSchemas, names, nil
}

// getTableData gets all the data for a single table, returning a string slice where each element is an insert statement
// for the data.
func getTableData(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	var statements []string

	// Query all rows.
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s ORDER BY rowid", table))
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch rows for table %q: %w", table, err)
	}

	defer func() { _ = rows.Close() }()

	// Get the column names.
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("Failed to get columns for table %q: %w", table, err)
	}

	// Generate an INSERT statement for each row.
	for i := 0; rows.Next(); i++ {
		raw := make([]any, len(columns)) // Raw column values
		row := make([]any, len(columns))
		for i := range raw {
			row[i] = &raw[i]
		}

		err := rows.Scan(row...)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan row %d in table %q: %w", i, table, err)
		}

		values := make([]string, len(columns))
		for j, v := range raw {
			switch v := v.(type) {
			case int64:
				values[j] = strconv.FormatInt(v, 10)
			case string:
				// This is based on logic from dump_callback in sqlite source for sqlite3_db_dump function.
				v = fmt.Sprintf("'%s'", strings.ReplaceAll(v, "'", "''"))

				if strings.Contains(v, "\r") {
					v = "replace(" + strings.ReplaceAll(v, "\r", "\\r") + ",'\\r',char(13))"
				}

				if strings.Contains(v, "\n") {
					v = "replace(" + strings.ReplaceAll(v, "\n", "\\n") + ",'\\n',char(10))"
				}

				values[j] = v

			case []byte:
				values[j] = fmt.Sprintf("'%s'", string(v))
			case time.Time:
				// Try and match the sqlite3 .dump output format.
				format := "2006-01-02 15:04:05"

				if v.Nanosecond() > 0 {
					format = format + ".000000000"
				}

				format = format + "-07:00"

				values[j] = "'" + v.Format(format) + "'"
			default:
				if v != nil {
					return nil, fmt.Errorf("Bad type in column %q of row %d in table %q", columns[j], i, table)
				}

				values[j] = "NULL"
			}
		}

		statement := fmt.Sprintf("INSERT INTO %s VALUES(%s);", table, strings.Join(values, ","))
		statements = append(statements, statement)
	}

	return statements, nil
}
