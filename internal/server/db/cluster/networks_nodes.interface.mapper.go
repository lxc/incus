//go:build linux && cgo && !agent

package cluster

import "context"

// NetworkNodeGenerated is an interface of generated methods for NetworkNode.
type NetworkNodeGenerated interface {
	// GetNetworkNodes returns all available network_nodes.
	// generator: network_node GetMany
	GetNetworkNodes(ctx context.Context, db dbtx, filters ...NetworkNodeFilter) ([]NetworkNode, error)
}
