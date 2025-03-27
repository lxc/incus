//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkAddressSetGenerated is an interface of generated methods for NetworkAddressSet.
type NetworkAddressSetGenerated interface {
	// GetNetworkAddressSetID return the ID of the network_address_set with the given key.
	// generator: network_address_set ID
	GetNetworkAddressSetID(ctx context.Context, db tx, project string, name string) (int64, error)

	// NetworkAddressSetExists checks if a network_address_set with the given key exists.
	// generator: network_address_set Exists
	NetworkAddressSetExists(ctx context.Context, db dbtx, project string, name string) (bool, error)

	// GetNetworkAddressSetConfig returns all available NetworkAddressSet Config
	// generator: network_address_set GetMany
	GetNetworkAddressSetConfig(ctx context.Context, db tx, networkAddressSetID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkAddressSets returns all available network_address_sets.
	// generator: network_address_set GetMany
	GetNetworkAddressSets(ctx context.Context, db dbtx, filters ...NetworkAddressSetFilter) ([]NetworkAddressSet, error)

	// GetNetworkAddressSet returns the network_address_set with the given key.
	// generator: network_address_set GetOne
	GetNetworkAddressSet(ctx context.Context, db dbtx, project string, name string) (*NetworkAddressSet, error)

	// CreateNetworkAddressSetConfig adds new network_address_set Config to the database.
	// generator: network_address_set Create
	CreateNetworkAddressSetConfig(ctx context.Context, db dbtx, networkAddressSetID int64, config map[string]string) error

	// CreateNetworkAddressSet adds a new network_address_set to the database.
	// generator: network_address_set Create
	CreateNetworkAddressSet(ctx context.Context, db dbtx, object NetworkAddressSet) (int64, error)

	// RenameNetworkAddressSet renames the network_address_set matching the given key parameters.
	// generator: network_address_set Rename
	RenameNetworkAddressSet(ctx context.Context, db dbtx, project string, name string, to string) error

	// UpdateNetworkAddressSetConfig updates the network_address_set Config matching the given key parameters.
	// generator: network_address_set Update
	UpdateNetworkAddressSetConfig(ctx context.Context, db tx, networkAddressSetID int64, config map[string]string) error

	// UpdateNetworkAddressSet updates the network_address_set matching the given key parameters.
	// generator: network_address_set Update
	UpdateNetworkAddressSet(ctx context.Context, db tx, project string, name string, object NetworkAddressSet) error

	// DeleteNetworkAddressSet deletes the network_address_set matching the given key parameters.
	// generator: network_address_set DeleteOne-by-Project-and-Name
	DeleteNetworkAddressSet(ctx context.Context, db dbtx, project string, name string) error
}
