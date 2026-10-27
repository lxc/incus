//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkPeerGenerated is an interface of generated methods for NetworkPeer.
type NetworkPeerGenerated interface {
	// GetNetworkPeerConfig returns all available NetworkPeer Config
	// generator: network_peer GetMany
	GetNetworkPeerConfig(ctx context.Context, db tx, networkPeerID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkPeers returns all available network_peers.
	// generator: network_peer GetMany
	GetNetworkPeers(ctx context.Context, db dbtx, filters ...NetworkPeerFilter) ([]NetworkPeer, error)

	// GetNetworkPeer returns the network_peer with the given key.
	// generator: network_peer GetOne
	GetNetworkPeer(ctx context.Context, db dbtx, networkID int64, name string) (*NetworkPeer, error)

	// NetworkPeerExists checks if a network_peer with the given key exists.
	// generator: network_peer Exists
	NetworkPeerExists(ctx context.Context, db dbtx, networkID int64, name string) (bool, error)

	// CreateNetworkPeerConfig adds new network_peer Config to the database.
	// generator: network_peer Create
	CreateNetworkPeerConfig(ctx context.Context, db dbtx, networkPeerID int64, config map[string]string) error

	// CreateNetworkPeer adds a new network_peer to the database.
	// generator: network_peer Create
	CreateNetworkPeer(ctx context.Context, db dbtx, object NetworkPeer) (int64, error)

	// GetNetworkPeerID return the ID of the network_peer with the given key.
	// generator: network_peer ID
	GetNetworkPeerID(ctx context.Context, db tx, networkID int64, name string) (int64, error)

	// DeleteNetworkPeer deletes the network_peer matching the given key parameters.
	// generator: network_peer DeleteOne-by-NetworkID-and-ID
	DeleteNetworkPeer(ctx context.Context, db dbtx, networkID int64, id int64) error

	// UpdateNetworkPeerConfig updates the network_peer Config matching the given key parameters.
	// generator: network_peer Update
	UpdateNetworkPeerConfig(ctx context.Context, db tx, networkPeerID int64, config map[string]string) error

	// UpdateNetworkPeer updates the network_peer matching the given key parameters.
	// generator: network_peer Update
	UpdateNetworkPeer(ctx context.Context, db tx, networkID int64, name string, object NetworkPeer) error
}
