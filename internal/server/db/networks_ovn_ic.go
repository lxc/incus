//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/shared/api"
)

// OVNInterconnectRequirement binds an origin to its integration and the backend that accepted its writes.
type OVNInterconnectRequirement struct {
	Token         string
	IntegrationID int64
	RootUUID      string
}

// RecordOVNInterconnectOperation requires an acknowledged generation before exposing the client to peer mutations.
func (c *ClusterTx) RecordOVNInterconnectOperation(ctx context.Context, token string, integrationName string, rootUUID string) error {
	id, err := uuid.Parse(rootUUID)
	if err != nil || id == uuid.Nil {
		return api.StatusErrorf(http.StatusConflict, "Invalid OVN interconnect backend identity")
	}

	result, err := c.tx.ExecContext(ctx, `UPDATE networks_ovn_operations SET ic_integration_id=(SELECT id FROM networks_integrations WHERE name=?), ic_root=? WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=? AND node_id=? AND token=? AND operation IN ('peer-create', 'peer-delete') AND backend_fenced=1 AND abandoned=0 AND EXISTS (SELECT 1 FROM networks_integrations WHERE name=?) AND ((ic_integration_id IS NULL AND ic_root='') OR (ic_integration_id=(SELECT id FROM networks_integrations WHERE name=?) AND ic_root=?))`, integrationName, rootUUID, api.ProjectDefaultName, OVNPeerOperationName, c.nodeID, token, integrationName, integrationName, rootUUID)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "OVN interconnect reservation or backend identity changed before dispatch")
	}

	return nil
}

// PreviousOVNInterconnectRequirements returns only descriptors belonging to the startup snapshot's local origins.
func (c *ClusterTx) PreviousOVNInterconnectRequirements(ctx context.Context, previous OVNPreviousWork) ([]OVNInterconnectRequirement, error) {
	if previous.NodeID != c.nodeID {
		return nil, api.StatusErrorf(http.StatusConflict, "OVN previous work belongs to another member")
	}

	requirements := make([]OVNInterconnectRequirement, 0)
	for _, token := range previous.OriginTokens {
		var integrationID sql.NullInt64
		var rootUUID string
		err := c.tx.QueryRowContext(ctx, `SELECT ic_integration_id, ic_root FROM networks_ovn_operations WHERE token=? AND node_id=? AND backend_fenced=1`, token, c.nodeID).Scan(&integrationID, &rootUUID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if integrationID.Valid != (rootUUID != "") {
			return nil, api.StatusErrorf(http.StatusConflict, "Incomplete OVN interconnect recovery identity")
		}

		if !integrationID.Valid {
			continue
		}

		requirements = append(requirements, OVNInterconnectRequirement{Token: token, IntegrationID: integrationID.Int64, RootUUID: rootUUID})
	}

	return requirements, nil
}

// ValidateOVNInterconnectRequirement prevents a different integration or backend from acknowledging captured work.
func (c *ClusterTx) ValidateOVNInterconnectRequirement(ctx context.Context, requirement OVNInterconnectRequirement, rootUUID string) error {
	var matches bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE token=? AND node_id=? AND backend_fenced=1 AND ic_integration_id=? AND ic_root=?)`, requirement.Token, c.nodeID, requirement.IntegrationID, requirement.RootUUID).Scan(&matches)
	if err != nil {
		return err
	}

	if !matches || rootUUID != requirement.RootUUID {
		return api.StatusErrorf(http.StatusConflict, "OVN interconnect recovery backend does not match the captured origin")
	}

	return nil
}

// CompleteOVNInterconnectFence records the matching backend's acknowledgement without releasing the origin itself.
func (c *ClusterTx) CompleteOVNInterconnectFence(ctx context.Context, requirement OVNInterconnectRequirement, rootUUID string) error {
	err := c.ValidateOVNInterconnectRequirement(ctx, requirement, rootUUID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE networks_ovn_operations SET ic_integration_id=NULL, ic_root='' WHERE token=? AND node_id=? AND backend_fenced=1 AND ic_integration_id=? AND ic_root=?`, requirement.Token, c.nodeID, requirement.IntegrationID, requirement.RootUUID)
	return err
}
