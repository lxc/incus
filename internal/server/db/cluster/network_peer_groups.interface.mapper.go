//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkPeerGroupGenerated is an interface of generated methods for NetworkPeerGroup.
type NetworkPeerGroupGenerated interface {
	// GetNetworkPeerGroups returns all available network_peer_groups.
	// generator: network_peer_group GetMany
	GetNetworkPeerGroups(ctx context.Context, db dbtx, filters ...NetworkPeerGroupFilter) ([]NetworkPeerGroup, error)

	// GetNetworkPeerGroup returns the network_peer_group with the given key.
	// generator: network_peer_group GetOne
	GetNetworkPeerGroup(ctx context.Context, db dbtx, name string) (*NetworkPeerGroup, error)

	// NetworkPeerGroupExists checks if a network_peer_group with the given key exists.
	// generator: network_peer_group Exists
	NetworkPeerGroupExists(ctx context.Context, db dbtx, name string) (bool, error)

	// CreateNetworkPeerGroup adds a new network_peer_group to the database.
	// generator: network_peer_group Create
	CreateNetworkPeerGroup(ctx context.Context, db dbtx, object NetworkPeerGroup) (int64, error)

	// GetNetworkPeerGroupID return the ID of the network_peer_group with the given key.
	// generator: network_peer_group ID
	GetNetworkPeerGroupID(ctx context.Context, db tx, name string) (int64, error)

	// DeleteNetworkPeerGroup deletes the network_peer_group matching the given key parameters.
	// generator: network_peer_group DeleteOne-by-Name
	DeleteNetworkPeerGroup(ctx context.Context, db dbtx, name string) error
}
