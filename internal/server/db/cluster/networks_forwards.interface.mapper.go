//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkForwardGenerated is an interface of generated methods for NetworkForward.
type NetworkForwardGenerated interface {
	// GetNetworkForwardConfig returns all available NetworkForward Config
	// generator: network_forward GetMany
	GetNetworkForwardConfig(ctx context.Context, db tx, networkForwardID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkForwards returns all available network_forwards.
	// generator: network_forward GetMany
	GetNetworkForwards(ctx context.Context, db dbtx, filters ...NetworkForwardFilter) ([]NetworkForward, error)

	// GetNetworkForward returns the network_forward with the given key.
	// generator: network_forward GetOne
	GetNetworkForward(ctx context.Context, db dbtx, networkID int64, listenAddress string) (*NetworkForward, error)

	// GetNetworkForwardID return the ID of the network_forward with the given key.
	// generator: network_forward ID
	GetNetworkForwardID(ctx context.Context, db tx, networkID int64, listenAddress string) (int64, error)

	// CreateNetworkForwardConfig adds new network_forward Config to the database.
	// generator: network_forward Create
	CreateNetworkForwardConfig(ctx context.Context, db dbtx, networkForwardID int64, config map[string]string) error

	// CreateNetworkForward adds a new network_forward to the database.
	// generator: network_forward Create
	CreateNetworkForward(ctx context.Context, db dbtx, object NetworkForward) (int64, error)

	// UpdateNetworkForwardConfig updates the network_forward Config matching the given key parameters.
	// generator: network_forward Update
	UpdateNetworkForwardConfig(ctx context.Context, db tx, networkForwardID int64, config map[string]string) error

	// UpdateNetworkForward updates the network_forward matching the given key parameters.
	// generator: network_forward Update
	UpdateNetworkForward(ctx context.Context, db tx, networkID int64, listenAddress string, object NetworkForward) error

	// DeleteNetworkForward deletes the network_forward matching the given key parameters.
	// generator: network_forward DeleteOne-by-NetworkID-and-ID
	DeleteNetworkForward(ctx context.Context, db dbtx, networkID int64, id int64) error
}
