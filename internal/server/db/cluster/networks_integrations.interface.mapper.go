//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// NetworkIntegrationGenerated is an interface of generated methods for NetworkIntegration.
type NetworkIntegrationGenerated interface {
	// GetNetworkIntegrationConfig returns all available NetworkIntegration Config
	// generator: network_integration GetMany
	GetNetworkIntegrationConfig(ctx context.Context, tx *sql.Tx, networkIntegrationID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkIntegrations returns all available network_integrations.
	// generator: network_integration GetMany
	GetNetworkIntegrations(ctx context.Context, tx *sql.Tx, filters ...NetworkIntegrationFilter) ([]NetworkIntegration, error)

	// GetNetworkIntegration returns the network_integration with the given key.
	// generator: network_integration GetOne
	GetNetworkIntegration(ctx context.Context, tx *sql.Tx, name string) (*NetworkIntegration, error)

	// NetworkIntegrationExists checks if a network_integration with the given key exists.
	// generator: network_integration Exists
	NetworkIntegrationExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateNetworkIntegrationConfig adds new network_integration Config to the database.
	// generator: network_integration Create
	CreateNetworkIntegrationConfig(ctx context.Context, tx *sql.Tx, networkIntegrationID int64, config map[string]string) error

	// CreateNetworkIntegration adds a new network_integration to the database.
	// generator: network_integration Create
	CreateNetworkIntegration(ctx context.Context, tx *sql.Tx, object NetworkIntegration) (int64, error)

	// GetNetworkIntegrationID return the ID of the network_integration with the given key.
	// generator: network_integration ID
	GetNetworkIntegrationID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// RenameNetworkIntegration renames the network_integration matching the given key parameters.
	// generator: network_integration Rename
	RenameNetworkIntegration(ctx context.Context, tx *sql.Tx, name string, to string) error

	// DeleteNetworkIntegration deletes the network_integration matching the given key parameters.
	// generator: network_integration DeleteOne-by-Name
	DeleteNetworkIntegration(ctx context.Context, tx *sql.Tx, name string) error

	// UpdateNetworkIntegrationConfig updates the network_integration Config matching the given key parameters.
	// generator: network_integration Update
	UpdateNetworkIntegrationConfig(ctx context.Context, tx *sql.Tx, networkIntegrationID int64, config map[string]string) error

	// UpdateNetworkIntegration updates the network_integration matching the given key parameters.
	// generator: network_integration Update
	UpdateNetworkIntegration(ctx context.Context, tx *sql.Tx, name string, object NetworkIntegration) error
}
