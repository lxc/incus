//go:build linux && cgo && !agent

package cluster

import "context"

// WarningGenerated is an interface of generated methods for Warning.
type WarningGenerated interface {
	// GetWarnings returns all available warnings.
	// generator: warning GetMany
	GetWarnings(ctx context.Context, db dbtx, filters ...WarningFilter) ([]Warning, error)

	// GetWarning returns the warning with the given key.
	// generator: warning GetOne-by-UUID
	GetWarning(ctx context.Context, db dbtx, uuid string) (*Warning, error)

	// DeleteWarning deletes the warning matching the given key parameters.
	// generator: warning DeleteOne-by-UUID
	DeleteWarning(ctx context.Context, db dbtx, uuid string) error

	// DeleteWarnings deletes the warning matching the given key parameters.
	// generator: warning DeleteMany-by-EntityTypeCode-and-EntityID
	DeleteWarnings(ctx context.Context, db dbtx, entityTypeCode int, entityID int) error

	// GetWarningID return the ID of the warning with the given key.
	// generator: warning ID
	GetWarningID(ctx context.Context, db tx, uuid string) (int64, error)

	// WarningExists checks if a warning with the given key exists.
	// generator: warning Exists
	WarningExists(ctx context.Context, db dbtx, uuid string) (bool, error)
}
