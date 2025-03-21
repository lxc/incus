//go:build linux && cgo && !agent

package cluster

import "context"

// ConfigGenerated is an interface of generated methods for Config.
type ConfigGenerated interface {
	// GetConfig returns all available config.
	// generator: config GetMany
	GetConfig(ctx context.Context, db dbtx, parentTablePrefix string, parentColumnPrefix string, filters ...ConfigFilter) (map[int]map[string]string, error)

	// CreateConfig adds a new config to the database.
	// generator: config Create
	CreateConfig(ctx context.Context, db dbtx, parentTablePrefix string, parentColumnPrefix string, object Config) error

	// UpdateConfig updates the config matching the given key parameters.
	// generator: config Update
	UpdateConfig(ctx context.Context, db dbtx, parentTablePrefix string, parentColumnPrefix string, referenceID int, config map[string]string) error

	// DeleteConfig deletes the config matching the given key parameters.
	// generator: config DeleteMany
	DeleteConfig(ctx context.Context, db dbtx, parentTablePrefix string, parentColumnPrefix string, referenceID int) error
}
