//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkZoneRecordGenerated is an interface of generated methods for NetworkZoneRecord.
type NetworkZoneRecordGenerated interface {
	// GetNetworkZoneRecordConfig returns all available NetworkZoneRecord Config
	// generator: NetworkZoneRecord GetMany
	GetNetworkZoneRecordConfig(ctx context.Context, db tx, networkZoneRecordID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkZoneRecords returns all available NetworkZoneRecords.
	// generator: NetworkZoneRecord GetMany
	GetNetworkZoneRecords(ctx context.Context, db dbtx, filters ...NetworkZoneRecordFilter) ([]NetworkZoneRecord, error)

	// GetNetworkZoneRecord returns the NetworkZoneRecord with the given key.
	// generator: NetworkZoneRecord GetOne
	GetNetworkZoneRecord(ctx context.Context, db dbtx, networkZoneID int, name string) (*NetworkZoneRecord, error)

	// NetworkZoneRecordExists checks if a NetworkZoneRecord with the given key exists.
	// generator: NetworkZoneRecord Exists
	NetworkZoneRecordExists(ctx context.Context, db dbtx, networkZoneID int, name string) (bool, error)

	// CreateNetworkZoneRecordConfig adds new NetworkZoneRecord Config to the database.
	// generator: NetworkZoneRecord Create
	CreateNetworkZoneRecordConfig(ctx context.Context, db dbtx, networkZoneRecordID int64, config map[string]string) error

	// CreateNetworkZoneRecord adds a new NetworkZoneRecord to the database.
	// generator: NetworkZoneRecord Create
	CreateNetworkZoneRecord(ctx context.Context, db dbtx, object NetworkZoneRecord) (int64, error)

	// GetNetworkZoneRecordID return the ID of the NetworkZoneRecord with the given key.
	// generator: NetworkZoneRecord ID
	GetNetworkZoneRecordID(ctx context.Context, db tx, networkZoneID int, name string) (int64, error)

	// RenameNetworkZoneRecord renames the NetworkZoneRecord matching the given key parameters.
	// generator: NetworkZoneRecord Rename
	RenameNetworkZoneRecord(ctx context.Context, db dbtx, networkZoneID int, name string, to string) error

	// UpdateNetworkZoneRecordConfig updates the NetworkZoneRecord Config matching the given key parameters.
	// generator: NetworkZoneRecord Update
	UpdateNetworkZoneRecordConfig(ctx context.Context, db tx, networkZoneRecordID int64, config map[string]string) error

	// UpdateNetworkZoneRecord updates the NetworkZoneRecord matching the given key parameters.
	// generator: NetworkZoneRecord Update
	UpdateNetworkZoneRecord(ctx context.Context, db tx, networkZoneID int, name string, object NetworkZoneRecord) error

	// DeleteNetworkZoneRecord deletes the NetworkZoneRecord matching the given key parameters.
	// generator: NetworkZoneRecord DeleteOne-by-NetworkZoneID-and-ID
	DeleteNetworkZoneRecord(ctx context.Context, db dbtx, networkZoneID int, id int) error
}
