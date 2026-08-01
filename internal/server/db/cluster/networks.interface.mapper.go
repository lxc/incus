//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkGenerated is an interface of generated methods for Network.
type NetworkGenerated interface {
	// GetNetworks returns all available networks.
	// generator: network GetMany
	GetNetworks(ctx context.Context, db dbtx, filters ...NetworkFilter) ([]Network, error)
}
