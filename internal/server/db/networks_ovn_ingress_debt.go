//go:build linux && cgo && !agent

package db

import (
	"context"
	"net/http"

	"github.com/lxc/incus/v7/shared/api"
)

// EnsureOVNNICCleanupDebtComplete gates a live configuration update without acknowledging live claims.
func (c *ClusterTx) EnsureOVNNICCleanupDebtComplete(ctx context.Context, networkID int64) error {
	if networkID <= 0 {
		return api.StatusErrorf(http.StatusBadRequest, "Invalid OVN network cleanup identity")
	}

	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup_networks WHERE source_node_id=? AND network_id=?)`, c.nodeID, networkID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "OVN network has unacknowledged source NIC cleanup")
	}

	return nil
}
