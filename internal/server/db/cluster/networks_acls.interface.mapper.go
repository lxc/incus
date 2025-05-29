//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkACLGenerated is an interface of generated methods for NetworkACL.
type NetworkACLGenerated interface {
	// GetNetworkACLConfig returns all available NetworkACL Config
	// generator: NetworkACL GetMany
	GetNetworkACLConfig(ctx context.Context, db tx, networkACLID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkACLs returns all available NetworkACLs.
	// generator: NetworkACL GetMany
	GetNetworkACLs(ctx context.Context, db dbtx, filters ...NetworkACLFilter) ([]NetworkACL, error)

	// GetNetworkACL returns the NetworkACL with the given key.
	// generator: NetworkACL GetOne
	GetNetworkACL(ctx context.Context, db dbtx, project string, name string) (*NetworkACL, error)

	// NetworkACLExists checks if a NetworkACL with the given key exists.
	// generator: NetworkACL Exists
	NetworkACLExists(ctx context.Context, db dbtx, project string, name string) (bool, error)

	// CreateNetworkACLConfig adds new NetworkACL Config to the database.
	// generator: NetworkACL Create
	CreateNetworkACLConfig(ctx context.Context, db dbtx, networkACLID int64, config map[string]string) error

	// CreateNetworkACL adds a new NetworkACL to the database.
	// generator: NetworkACL Create
	CreateNetworkACL(ctx context.Context, db dbtx, object NetworkACL) (int64, error)

	// GetNetworkACLID return the ID of the NetworkACL with the given key.
	// generator: NetworkACL ID
	GetNetworkACLID(ctx context.Context, db tx, project string, name string) (int64, error)

	// RenameNetworkACL renames the NetworkACL matching the given key parameters.
	// generator: NetworkACL Rename
	RenameNetworkACL(ctx context.Context, db dbtx, project string, name string, to string) error

	// UpdateNetworkACLConfig updates the NetworkACL Config matching the given key parameters.
	// generator: NetworkACL Update
	UpdateNetworkACLConfig(ctx context.Context, db tx, networkACLID int64, config map[string]string) error

	// UpdateNetworkACL updates the NetworkACL matching the given key parameters.
	// generator: NetworkACL Update
	UpdateNetworkACL(ctx context.Context, db tx, project string, name string, object NetworkACL) error

	// DeleteNetworkACL deletes the NetworkACL matching the given key parameters.
	// generator: NetworkACL DeleteOne-by-ID
	DeleteNetworkACL(ctx context.Context, db dbtx, id int) error
}
