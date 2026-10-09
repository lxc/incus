//go:build linux && cgo && !agent

package ovn

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/api"
)

func TestICNBRecoveryRejectsDifferentRealBackend(t *testing.T) {
	first, firstRoot := icTestServer(t)
	second, secondRoot := icTestServer(t)
	require.NotEqual(t, firstRoot, secondRoot)
	original, err := NewICNB("unix:"+first, "", "", "", "recovery-member")
	require.NoError(t, err)
	t.Cleanup(original.client.Close)
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	_, err = cluster.CreateNetworkIntegration(ctx, tx.Tx(), cluster.NetworkIntegration{Name: "ic", Type: 0})
	require.NoError(t, err)
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "origin", "peer-delete", true))
	require.NoError(t, tx.RecordOVNInterconnectOperation(ctx, "origin", "ic", original.BackendID()))
	previous, err := tx.SnapshotLocalOVNFencedWork(ctx)
	require.NoError(t, err)
	requirements, err := tx.PreviousOVNInterconnectRequirements(ctx, previous)
	require.NoError(t, err)
	require.Len(t, requirements, 1)
	wrong, err := NewICNB("unix:"+second, "", "", "", "recovery-member")
	require.NoError(t, err)
	t.Cleanup(wrong.client.Close)
	require.True(t, api.StatusErrorCheck(tx.CompleteOVNInterconnectFence(ctx, requirements[0], wrong.BackendID()), http.StatusConflict))
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "origin", token)
	correct, err := NewICNB("unix:"+first, "", "", "", "recovery-member")
	require.NoError(t, err)
	t.Cleanup(correct.client.Close)
	require.NoError(t, tx.CompleteOVNInterconnectFence(ctx, requirements[0], correct.BackendID()))
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	token, err = tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Empty(t, token)
}
