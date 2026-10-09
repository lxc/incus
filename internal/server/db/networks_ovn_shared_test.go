//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNSharedBackendOwnership(t *testing.T) {
	for _, operation := range []string{"acl-config", "address-set-config"} {
		t.Run(operation, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()

			require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "old", operation, false))
			pending, err := tx.HasLocalFencedOVNWork(ctx)
			require.NoError(t, err)
			require.False(t, pending)
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "network", "delete", "delete"), http.StatusConflict))
			require.NoError(t, tx.ClearLocalOVNBackendConfig(ctx))

			// A stale writer cannot promote a replacement reservation after configuration-only crash recovery.
			require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "current", operation, false))
			require.True(t, api.StatusErrorCheck(tx.PromoteOVNSharedOperation(ctx, "old", operation), http.StatusConflict))
			require.True(t, api.StatusErrorCheck(tx.PromoteOVNSharedOperation(ctx, "current", "peer-delete"), http.StatusConflict))
			require.NoError(t, tx.PromoteOVNSharedOperation(ctx, "current", operation))
			pending, err = tx.HasLocalFencedOVNWork(ctx)
			require.NoError(t, err)
			require.True(t, pending)

			// Once backend work is possible, configuration-only recovery cannot permit name reuse.
			require.NoError(t, tx.ClearLocalOVNBackendConfig(ctx))
			token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
			require.NoError(t, err)
			require.Equal(t, "current", token)
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "create", "create"), http.StatusConflict))
			require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "current"))
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "create", "create"))
		})
	}
}

func TestOVNSharedPromotionRequiresLocalLiveReservation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	other, err := tx.CreateNode("other", "10.0.0.2:8443")
	require.NoError(t, err)
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "work", "acl-config", false))
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=? WHERE token='work'", other)
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(tx.PromoteOVNSharedOperation(ctx, "work", "acl-config"), http.StatusConflict))
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=?, abandoned=1 WHERE token='work'", tx.GetNodeID())
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(tx.PromoteOVNSharedOperation(ctx, "work", "acl-config"), http.StatusConflict))
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET abandoned=0, name='ordinary-network' WHERE token='work'")
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(tx.PromoteOVNSharedOperation(ctx, "work", "acl-config"), http.StatusConflict))
}
