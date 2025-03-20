//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkIntegrationGenerated is an interface of generated methods for NetworkIntegration.
type NetworkIntegrationGenerated interface {
	// GetNetworkIntegrationConfig returns all available NetworkIntegration Config
	// generator: network_integration GetMany
	GetNetworkIntegrationConfig(ctx context.Context, db tx, networkIntegrationID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkIntegrations returns all available network_integrations.
	// generator: network_integration GetMany
	GetNetworkIntegrations(ctx context.Context, db dbtx, filters ...NetworkIntegrationFilter) ([]NetworkIntegration, error)

	// GetNetworkIntegration returns the network_integration with the given key.
	// generator: network_integration GetOne
	GetNetworkIntegration(ctx context.Context, db dbtx, name string) (*NetworkIntegration, error)

	// NetworkIntegrationExists checks if a network_integration with the given key exists.
	// generator: network_integration Exists
	NetworkIntegrationExists(ctx context.Context, db dbtx, name string) (bool, error)

	// CreateNetworkIntegrationConfig adds new network_integration Config to the database.
	// generator: network_integration Create
	CreateNetworkIntegrationConfig(ctx context.Context, db dbtx, networkIntegrationID int64, config map[string]string) error

	// CreateNetworkIntegration adds a new network_integration to the database.
	// generator: network_integration Create
	CreateNetworkIntegration(ctx context.Context, db dbtx, object NetworkIntegration) (int64, error)

	// GetNetworkIntegrationID return the ID of the network_integration with the given key.
	// generator: network_integration ID
	GetNetworkIntegrationID(ctx context.Context, db tx, name string) (int64, error)

	// RenameNetworkIntegration renames the network_integration matching the given key parameters.
	// generator: network_integration Rename
	RenameNetworkIntegration(ctx context.Context, db dbtx, name string, to string) error

	// DeleteNetworkIntegration deletes the network_integration matching the given key parameters.
	// generator: network_integration DeleteOne-by-Name
	DeleteNetworkIntegration(ctx context.Context, db dbtx, name string) error

	// UpdateNetworkIntegrationConfig updates the network_integration Config matching the given key parameters.
	// generator: network_integration Update
	UpdateNetworkIntegrationConfig(ctx context.Context, db tx, networkIntegrationID int64, config map[string]string) error

	// UpdateNetworkIntegration updates the network_integration matching the given key parameters.
	// generator: network_integration Update
	UpdateNetworkIntegration(ctx context.Context, db tx, name string, object NetworkIntegration) error
}
