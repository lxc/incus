//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
)

// TestOVNReferencesInapplicable covers clusters upgraded or touched without any OVN network: ACL and
// address-set changes need no OVN client until an OVN network definition exists.
func TestOVNReferencesInapplicable(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "UPDATE ovn_reference_applicability SET state='unknown', nb_root='' WHERE id=1")
		require.NoError(t, err)
		inapplicable, err := tx.OVNReferencesInapplicable(ctx)
		require.NoError(t, err)
		require.True(t, inapplicable)
		require.NoError(t, tx.CheckOVNReferencePublication(ctx, ""))
		_, err = tx.CreateNetwork(ctx, "default", "ovn1", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		inapplicable, err = tx.OVNReferencesInapplicable(ctx)
		require.NoError(t, err)
		require.False(t, inapplicable)
		require.Error(t, tx.CheckOVNReferencePublication(ctx, ""))
		return nil
	})
	require.NoError(t, err)
}

// A targeted OVN definition that was never created holds no OVN reference, until its create
// operation begins.
func TestOVNReferencesInapplicablePendingDefinition(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "UPDATE ovn_reference_applicability SET state='unknown', nb_root='' WHERE id=1")
		require.NoError(t, err)
		require.NoError(t, tx.CreatePendingNetwork(ctx, "none", "default", "ovn1", "", db.NetworkTypeOVN, nil))
		inapplicable, err := tx.OVNReferencesInapplicable(ctx)
		require.NoError(t, err)
		require.True(t, inapplicable)

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation) VALUES (1, 'ovn1', 1, 'token', 'create')")
		require.NoError(t, err)
		inapplicable, err = tx.OVNReferencesInapplicable(ctx)
		require.NoError(t, err)
		require.False(t, inapplicable)
		return nil
	})
	require.NoError(t, err)
}
