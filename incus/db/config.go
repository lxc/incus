//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/cyphar/incus/lxd/db/query"
)

// Config fetches all LXD node-level config keys.
func (n *NodeTx) Config(ctx context.Context) (map[string]string, error) {
	return query.SelectConfig(ctx, n.tx, "config", "")
}

// UpdateConfig updates the given LXD node-level configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (n *NodeTx) UpdateConfig(values map[string]string) error {
	return query.UpdateConfig(n.tx, "config", values)
}

// Config fetches all LXD cluster config keys.
func (c *ClusterTx) Config(ctx context.Context) (map[string]string, error) {
	return query.SelectConfig(ctx, c.tx, "config", "")
}

// UpdateClusterConfig updates the given LXD cluster configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (c *ClusterTx) UpdateClusterConfig(values map[string]string) error {
	return query.UpdateConfig(c.tx, "config", values)
}
