//go:build linux && cgo && !agent

package db

import (
	"context"
	"net/http"

	"github.com/lxc/incus/v7/shared/api"
)

// PromoteOVNSharedOperation records backend ownership before a shared configuration writer dispatches OVN mutations.
func (c *ClusterTx) PromoteOVNSharedOperation(ctx context.Context, token string, operation string) error {
	referenceErr := c.BeginOVNReferenceActivation(ctx)
	if referenceErr != nil {
		return referenceErr
	}

	if operation != "acl-config" && operation != "address-set-config" {
		return api.StatusErrorf(http.StatusConflict, "Invalid OVN shared configuration operation %q", operation)
	}

	result, err := c.tx.ExecContext(ctx, `UPDATE networks_ovn_operations SET backend_fenced=1 WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=? AND node_id=? AND token=? AND operation=? AND backend_fenced=0 AND abandoned=0 AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token)`, api.ProjectDefaultName, OVNPeerOperationName, c.nodeID, token, operation)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "OVN shared configuration reservation changed before backend dispatch")
	}

	return nil
}
