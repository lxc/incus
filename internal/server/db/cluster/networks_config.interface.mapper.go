//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkConfigGenerated is an interface of generated methods for NetworkConfig.
type NetworkConfigGenerated interface {
	// GetNetworkConfig returns all available network_config.
	// generator: network_config GetMany
	GetNetworkConfig(ctx context.Context, db dbtx, filters ...NetworkConfigFilter) ([]NetworkConfig, error)
}
