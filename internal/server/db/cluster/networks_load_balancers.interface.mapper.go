//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkLoadBalancerGenerated is an interface of generated methods for NetworkLoadBalancer.
type NetworkLoadBalancerGenerated interface {
	// GetNetworkLoadBalancerConfig returns all available NetworkLoadBalancer Config
	// generator: network_load_balancer GetMany
	GetNetworkLoadBalancerConfig(ctx context.Context, db tx, networkLoadBalancerID int, filters ...ConfigFilter) (map[string]string, error)

	// GetNetworkLoadBalancers returns all available network_load_balancers.
	// generator: network_load_balancer GetMany
	GetNetworkLoadBalancers(ctx context.Context, db dbtx, filters ...NetworkLoadBalancerFilter) ([]NetworkLoadBalancer, error)

	// GetNetworkLoadBalancer returns the network_load_balancer with the given key.
	// generator: network_load_balancer GetOne
	GetNetworkLoadBalancer(ctx context.Context, db dbtx, networkID int64, listenAddress string) (*NetworkLoadBalancer, error)

	// GetNetworkLoadBalancerID return the ID of the network_load_balancer with the given key.
	// generator: network_load_balancer ID
	GetNetworkLoadBalancerID(ctx context.Context, db tx, networkID int64, listenAddress string) (int64, error)

	// CreateNetworkLoadBalancerConfig adds new network_load_balancer Config to the database.
	// generator: network_load_balancer Create
	CreateNetworkLoadBalancerConfig(ctx context.Context, db dbtx, networkLoadBalancerID int64, config map[string]string) error

	// CreateNetworkLoadBalancer adds a new network_load_balancer to the database.
	// generator: network_load_balancer Create
	CreateNetworkLoadBalancer(ctx context.Context, db dbtx, object NetworkLoadBalancer) (int64, error)

	// UpdateNetworkLoadBalancerConfig updates the network_load_balancer Config matching the given key parameters.
	// generator: network_load_balancer Update
	UpdateNetworkLoadBalancerConfig(ctx context.Context, db tx, networkLoadBalancerID int64, config map[string]string) error

	// UpdateNetworkLoadBalancer updates the network_load_balancer matching the given key parameters.
	// generator: network_load_balancer Update
	UpdateNetworkLoadBalancer(ctx context.Context, db tx, networkID int64, listenAddress string, object NetworkLoadBalancer) error

	// DeleteNetworkLoadBalancer deletes the network_load_balancer matching the given key parameters.
	// generator: network_load_balancer DeleteOne-by-NetworkID-and-ID
	DeleteNetworkLoadBalancer(ctx context.Context, db dbtx, networkID int64, id int64) error
}
