//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkZoneGenerated is an interface of generated methods for NetworkZone.
type NetworkZoneGenerated interface {
	// GetNetworkZoneConfig returns all available NetworkZone Config
	// generator: NetworkZone GetMany
	GetNetworkZoneConfig(ctx context.Context, db tx, networkZoneID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkZones returns all available NetworkZones.
	// generator: NetworkZone GetMany
	GetNetworkZones(ctx context.Context, db dbtx, filters ...NetworkZoneFilter) ([]NetworkZone, error)

	// GetNetworkZone returns the NetworkZone with the given key.
	// generator: NetworkZone GetOne
	GetNetworkZone(ctx context.Context, db dbtx, project string, name string) (*NetworkZone, error)

	// NetworkZoneExists checks if a NetworkZone with the given key exists.
	// generator: NetworkZone Exists
	NetworkZoneExists(ctx context.Context, db dbtx, project string, name string) (bool, error)

	// CreateNetworkZoneConfig adds new NetworkZone Config to the database.
	// generator: NetworkZone Create
	CreateNetworkZoneConfig(ctx context.Context, db dbtx, networkZoneID int64, config map[string]string) error

	// CreateNetworkZone adds a new NetworkZone to the database.
	// generator: NetworkZone Create
	CreateNetworkZone(ctx context.Context, db dbtx, object NetworkZone) (int64, error)

	// GetNetworkZoneID return the ID of the NetworkZone with the given key.
	// generator: NetworkZone ID
	GetNetworkZoneID(ctx context.Context, db tx, project string, name string) (int64, error)

	// RenameNetworkZone renames the NetworkZone matching the given key parameters.
	// generator: NetworkZone Rename
	RenameNetworkZone(ctx context.Context, db dbtx, project string, name string, to string) error

	// UpdateNetworkZoneConfig updates the NetworkZone Config matching the given key parameters.
	// generator: NetworkZone Update
	UpdateNetworkZoneConfig(ctx context.Context, db tx, networkZoneID int64, config map[string]string) error

	// UpdateNetworkZone updates the NetworkZone matching the given key parameters.
	// generator: NetworkZone Update
	UpdateNetworkZone(ctx context.Context, db tx, project string, name string, object NetworkZone) error

	// DeleteNetworkZone deletes the NetworkZone matching the given key parameters.
	// generator: NetworkZone DeleteOne-by-ID
	DeleteNetworkZone(ctx context.Context, db dbtx, id int) error
}
