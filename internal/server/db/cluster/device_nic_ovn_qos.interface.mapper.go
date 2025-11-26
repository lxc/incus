//go:build linux && cgo && !agent

package cluster

import "context"

// OVNQoSGenerated is an interface of generated methods for OVNQoS.
type OVNQoSGenerated interface {
	// GetOVNQoSs returns all available OVNQoSs.
	// generator: OVNQoS GetMany
	GetOVNQoSs(ctx context.Context, db dbtx, filters ...OVNQoSFilter) ([]OVNQoS, error)

	// GetOVNQoS returns the OVNQoS with the given key.
	// generator: OVNQoS GetOne
	GetOVNQoS(ctx context.Context, db dbtx, uuid string) (*OVNQoS, error)

	// OVNQoSExists checks if a OVNQoS with the given key exists.
	// generator: OVNQoS Exists
	OVNQoSExists(ctx context.Context, db dbtx, uuid string) (bool, error)

	// CreateOVNQoS adds a new OVNQoS to the database.
	// generator: OVNQoS Create
	CreateOVNQoS(ctx context.Context, db dbtx, object OVNQoS) (int64, error)

	// GetOVNQoSID return the ID of the OVNQoS with the given key.
	// generator: OVNQoS ID
	GetOVNQoSID(ctx context.Context, db tx, uuid string) (int64, error)

	// UpdateOVNQoS updates the OVNQoS matching the given key parameters.
	// generator: OVNQoS Update
	UpdateOVNQoS(ctx context.Context, db tx, uuid string, object OVNQoS) error

	// DeleteOVNQoS deletes the OVNQoS matching the given key parameters.
	// generator: OVNQoS DeleteOne-by-UUID
	DeleteOVNQoS(ctx context.Context, db dbtx, uuid string) error

	// DeleteOVNQoSs deletes the OVNQoS matching the given key parameters.
	// generator: OVNQoS DeleteMany-by-LogicalSwitch
	DeleteOVNQoSs(ctx context.Context, db dbtx, logicalSwitch string) error
}
