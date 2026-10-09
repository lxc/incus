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

// Child definitions and parent deletion are decided in their own transactions, so a child created
// concurrently with the parent's deletion is either seen by the parent or refused itself.
func TestOVNParentChildSerialization(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, "default", "parent", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.NoError(t, tx.CheckOVNParentCreated(ctx, "default", "parent"))
		require.True(t, api.StatusErrorCheck(tx.CheckOVNParentCreated(ctx, "default", "missing"), http.StatusNotFound))

		require.NoError(t, tx.CreatePendingNetwork(ctx, "none", "default", "child", "", db.NetworkTypeOVN, map[string]string{"parent": "parent"}))
		children, err := tx.OVNChildNetworks(ctx, "default", "parent")
		require.NoError(t, err)
		require.Equal(t, 1, children, "a child counts in any state")

		require.NoError(t, tx.NetworkDeleting("default", "parent"))
		require.True(t, api.StatusErrorCheck(tx.CheckOVNParentCreated(ctx, "default", "parent"), http.StatusConflict))
		return nil
	})
	require.NoError(t, err)
}

// Restore returns a prepared row of a network whose creation did not complete to Stopped, the
// state its create retry repairs, and only under the member's restore reservation.
func TestOVNLocalReturnStopped(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := tx.CreateNetwork(ctx, "default", "n", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.NoError(t, tx.NetworkErrored("default", "n"))
		_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=6 WHERE network_id=?", id)
		require.NoError(t, err)
		_, err = tx.Tx().ExecContext(ctx, "UPDATE nodes SET state=? WHERE id=1", db.ClusterMemberStateRestoring)
		require.NoError(t, err)

		require.Error(t, tx.OVNLocalReturnStopped(ctx, id, "token"), "requires the restore reservation")
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation) VALUES (1, 'n', 1, 'token', 'restore')")
		require.NoError(t, err)
		require.NoError(t, tx.OVNLocalReturnStopped(ctx, id, "token"))

		var state int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT state FROM networks_nodes WHERE network_id=?", id).Scan(&state))
		require.Equal(t, 7, state, "Stopped")

		require.NoError(t, tx.NetworkCreated("default", "n"))
		_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=6 WHERE network_id=?", id)
		require.NoError(t, err)
		require.Error(t, tx.OVNLocalReturnStopped(ctx, id, "token"), "a created network is restored by starting it")
		return nil
	})
	require.NoError(t, err)
}
