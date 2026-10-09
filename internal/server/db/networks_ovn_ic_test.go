//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNInterconnectRecoveryRequiresExactBackend(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	integrationID, err := cluster.CreateNetworkIntegration(ctx, tx.Tx(), cluster.NetworkIntegration{Name: "ic", Type: 0})
	require.NoError(t, err)
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "origin", "peer-create", true))
	previous, err := tx.SnapshotLocalOVNFencedWork(ctx)
	require.NoError(t, err)
	requirements, err := tx.PreviousOVNInterconnectRequirements(ctx, previous)
	require.NoError(t, err)
	require.Empty(t, requirements)
	root := uuid.NewString()
	require.True(t, api.StatusErrorCheck(tx.RecordOVNInterconnectOperation(ctx, "stale", "ic", root), http.StatusConflict))
	require.NoError(t, tx.RecordOVNInterconnectOperation(ctx, "origin", "ic", root))
	require.True(t, api.StatusErrorCheck(tx.RecordOVNInterconnectOperation(ctx, "origin", "ic", uuid.NewString()), http.StatusConflict))
	requirements, err = tx.PreviousOVNInterconnectRequirements(ctx, previous)
	require.NoError(t, err)
	require.Equal(t, []db.OVNInterconnectRequirement{{Token: "origin", IntegrationID: integrationID, RootUUID: root}}, requirements)
	require.True(t, api.StatusErrorCheck(tx.WithOVNPeerIntegrationOperation(ctx, func() error { return nil }), http.StatusConflict))
	_, err = tx.Tx().ExecContext(ctx, "DELETE FROM networks_integrations WHERE id=?", integrationID)
	require.Error(t, err)

	// Acknowledging the three ordinary backends cannot release an outstanding IC mutation.
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "origin", token)
	require.True(t, api.StatusErrorCheck(tx.CompleteOVNInterconnectFence(ctx, requirements[0], uuid.NewString()), http.StatusConflict))
	require.NoError(t, tx.CompleteOVNInterconnectFence(ctx, requirements[0], root))
	token, err = tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "origin", token)
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "new", "create"))
}

func TestOVNInterconnectDescriptorsDoNotUpgradeLegacyOrReplacementOrigins(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	_, err := cluster.CreateNetworkIntegration(ctx, tx.Tx(), cluster.NetworkIntegration{Name: "ic", Type: 0})
	require.NoError(t, err)
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "legacy", "peer-delete", false))
	require.True(t, api.StatusErrorCheck(tx.RecordOVNInterconnectOperation(ctx, "legacy", "ic", uuid.NewString()), http.StatusConflict))
	previous, err := tx.SnapshotLocalOVNFencedWork(ctx)
	require.NoError(t, err)
	require.Empty(t, previous.OriginTokens)
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "legacy", token)
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "legacy"))
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "replacement", "peer-delete", true))
	require.NoError(t, tx.RecordOVNInterconnectOperation(ctx, "replacement", "ic", uuid.NewString()))
	requirements, err := tx.PreviousOVNInterconnectRequirements(ctx, previous)
	require.NoError(t, err)
	require.Empty(t, requirements)
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	token, err = tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "replacement", token)
}
