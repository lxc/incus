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

func TestOVNNICCopiedClaimRequiresExactOriginalAcknowledgment(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, tokens := ovnCleanupFixture(t, tx)
	res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'original-claim',1,0,'',1)", tx.GetNodeID())
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	claim, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": a.NetworkIDs[0], "SourceNodeID": tx.GetNodeID(), "InstanceID": id, "InstanceUUID": a.InstanceUUID, "DeviceName": "eth0"})
	require.NoError(t, err)
	for key, value := range map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.last_state.ovn.host": string(claim)} {
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
		require.NoError(t, err)
	}

	payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "HostVolatile": map[string]string{"last_state.ovn.host": string(claim)}})
	require.NoError(t, err)
	a.Payload = string(payload)
	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, a, tokens))
	cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
	require.NoError(t, err)
	require.True(t, cleared)
	res, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'copied-claim',1,0,'',1)", tx.GetNodeID())
	require.NoError(t, err)
	copyID, err := res.LastInsertId()
	require.NoError(t, err)
	copyUUID := uuid.NewString()
	for key, value := range map[string]string{"volatile.uuid": copyUUID, "volatile.eth0.last_state.ovn.host": string(claim)} {
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", copyID, key, value)
		require.NoError(t, err)
	}

	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, copyUUID), "retirement alone cannot acknowledge the borrowed original claim")
	require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
	require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
	require.NoError(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, copyUUID), "only the exact original instance and raw claim in completed source payload may discharge quarantine")
	require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
	var copied string
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", copyID).Scan(&copied))
	require.Equal(t, string(claim), copied, "source acknowledgment must never edit or adopt the copied claim")
	_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", string(claim[:len(claim)-1])+`,"Alias":"replacement"}`, copyID)
	require.NoError(t, err)
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, copyUUID), "a different raw allocation has no acknowledgment")
}
