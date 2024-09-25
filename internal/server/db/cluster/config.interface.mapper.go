//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ConfigGenerated is an interface of generated methods for Config.
type ConfigGenerated interface {
	// GetConfig returns all available config.
	// generator: config GetMany
	GetConfig(ctx context.Context, tx *sql.Tx, parent string, filters ...ConfigFilter) (map[int]map[string]string, error)

	// CreateConfig adds a new config to the database.
	// generator: config Create
	CreateConfig(ctx context.Context, tx *sql.Tx, parent string, object Config) error

	// UpdateConfig updates the config matching the given key parameters.
	// generator: config Update
	UpdateConfig(ctx context.Context, tx *sql.Tx, parent string, referenceID int, config map[string]string) error

	// DeleteConfig deletes the config matching the given key parameters.
	// generator: config DeleteMany
	DeleteConfig(ctx context.Context, tx *sql.Tx, parent string, referenceID int) error
}
