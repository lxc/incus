//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// WarningGenerated is an interface of generated methods for Warning.
type WarningGenerated interface {
	// GetWarnings returns all available warnings.
	// generator: warning GetMany
	GetWarnings(ctx context.Context, tx *sql.Tx, filters ...WarningFilter) ([]Warning, error)

	// GetWarning returns the warning with the given key.
	// generator: warning GetOne-by-UUID
	GetWarning(ctx context.Context, tx *sql.Tx, uuid string) (*Warning, error)

	// DeleteWarning deletes the warning matching the given key parameters.
	// generator: warning DeleteOne-by-UUID
	DeleteWarning(ctx context.Context, tx *sql.Tx, uuid string) error

	// DeleteWarnings deletes the warning matching the given key parameters.
	// generator: warning DeleteMany-by-EntityTypeCode-and-EntityID
	DeleteWarnings(ctx context.Context, tx *sql.Tx, entityTypeCode int, entityID int) error

	// GetWarningID return the ID of the warning with the given key.
	// generator: warning ID
	GetWarningID(ctx context.Context, tx *sql.Tx, uuid string) (int64, error)

	// WarningExists checks if a warning with the given key exists.
	// generator: warning Exists
	WarningExists(ctx context.Context, tx *sql.Tx, uuid string) (bool, error)
}
