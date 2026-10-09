//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
)

func TestOVNIngressUpdateCleanupDebt(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	debt, tokens := ovnCleanupFixture(t, tx)
	networkID := debt.NetworkIDs[0]
	instanceUUID := uuid.NewString()
	res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'live-ingress-update',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", id, instanceUUID)
	require.NoError(t, err)
	claim, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": networkID, "SourceNodeID": tx.GetNodeID(), "InstanceUUID": instanceUUID, "InstanceID": id, "DeviceName": "eth0", "Alias": "incus-ovn-" + uuid.NewString()})
	require.NoError(t, err)
	producer := map[string]string{"host_name": "original-eth0", "last_state.ovn.host": string(claim)}
	for key, value := range producer {
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, "volatile.eth0."+key, value)
		require.NoError(t, err)
	}

	require.NoError(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, "eth0", networkID, producer))
	require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, networkID), "the old notification caller rejects the ordinary live workload")
	require.NoError(t, tx.EnsureOVNNICCleanupDebtComplete(ctx, networkID), "live configuration may continue without certifying cleanup")
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, instanceUUID), "maintenance still requires full cleanup")
	require.Error(t, tx.EnsureOVNNICCleanupDebtComplete(ctx, 0))
	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, debt, tokens))
	for _, id := range debt.NetworkIDs {
		require.Error(t, tx.EnsureOVNNICCleanupDebtComplete(ctx, id), "actual source cleanup debt still refuses the update")
	}

	require.NoError(t, tx.EnsureOVNNICCleanupDebtComplete(ctx, networkID+1000), "unrelated network debt is not adopted")
	pending, err := tx.OVNNICCleanups(ctx)
	require.NoError(t, err)
	require.Equal(t, []db.OVNNICCleanup{debt}, pending)
	var current string
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", id).Scan(&current))
	require.Equal(t, string(claim), current, "neither gate retires or rewrites the live claim")
	require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, networkID))
}
