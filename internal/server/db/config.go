//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

// Config fetches all server-level config keys.
func (n *NodeTx) Config(ctx context.Context) (map[string]string, error) {
	return query.SelectConfig(ctx, n.tx, "config", "")
}

// UpdateConfig updates the given server-level configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (n *NodeTx) UpdateConfig(values map[string]string) error {
	return query.UpdateConfig(n.tx, "config", values)
}

// Config fetches all cluster config keys.
func (c *ClusterTx) Config(ctx context.Context) (map[string]string, error) {
	return query.SelectConfig(ctx, c.tx, "config", "")
}

// UpdateClusterConfig updates the given cluster configuration keys in the
// config table. Config keys set to empty values will be deleted.
func (c *ClusterTx) UpdateClusterConfig(values map[string]string) error {
	return query.UpdateConfig(c.tx, "config", values)
}
