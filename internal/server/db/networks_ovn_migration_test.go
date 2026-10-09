//go:build linux && cgo && !agent

package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/shared/api"
)

func ovnMigrationFixture(t *testing.T, tx *ClusterTx) (OVNNICCleanup, map[int64]string, int, int64, string) {
	t.Helper()
	ctx := context.Background()
	network, err := tx.CreateNetwork(ctx, "default", "migration-net", "", NetworkTypeOVN, nil)
	require.NoError(t, err)
	require.NoError(t, tx.NetworkCreated("default", "migration-net"))
	require.NoError(t, tx.NetworkNodeCreated(network))
	token := uuid.NewString()
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", "migration-net", token, "nic"))
	result, err := tx.tx.ExecContext(ctx, `INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'moving',1,0,'',(SELECT id FROM projects WHERE name='default'))`, tx.nodeID)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	identity := uuid.NewString()
	host := map[string]string{"host_name": "source-host", "last_state.ovn.host": `{"original":true}`}
	for key, value := range map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": host["host_name"], "volatile.eth0.last_state.ovn.host": host["last_state.ovn.host"], "volatile.sibling.host_name": "sibling", "user.keep": "unrelated"} {
		_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
		require.NoError(t, err)
	}

	raw, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "NetworkID": network, "HostVolatile": host})
	require.NoError(t, err)
	a := OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: tx.nodeID, InstanceUUID: identity, DeviceName: "eth0", Version: 1, NetworkIDs: []int64{network}, Payload: string(raw)}
	tokens := map[int64]string{network: token}
	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, a, tokens))
	target, err := tx.CreateNode("migration-target", "192.0.2.10:8443")
	require.NoError(t, err)
	operation := uuid.NewString()
	require.NoError(t, tx.AuthorizeOVNNICMigration(ctx, operation, int(id), identity, target))
	return a, tokens, int(id), target, operation
}

func migrationTargetClaim(a OVNNICCleanup, id int, target int64) map[string]string {
	raw, _ := json.Marshal(map[string]any{"Version": 1, "NetworkID": a.NetworkIDs[0], "SourceNodeID": target, "InstanceID": id, "InstanceUUID": a.InstanceUUID, "DeviceName": a.DeviceName, "Alias": "incus-ovn-" + uuid.NewString()})
	return map[string]string{"host_name": "target-host", "last_state.ovn.host": string(raw)}
}

func migrationStagedChanges(claim map[string]string) map[string]string {
	changes := map[string]string{}
	for key, value := range claim {
		changes["volatile.eth0."+key] = value
	}

	return changes
}

func TestOVNNICMigrationTargetStageNoSourceOverwrite(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, _, id, target, operation := ovnMigrationFixture(t, tx)
	source := tx.nodeID
	tx.nodeID = target
	require.NoError(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], nil))
	claim := migrationTargetClaim(a, id, target)
	require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
	require.NoError(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], claim))
	require.Error(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], nil))
	require.Error(t, tx.EnsureOVNNICCleanupStart(ctx, a.InstanceUUID, "eth0", a.NetworkIDs[0], claim))
	var host, uuidCurrent string
	var node int64
	require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT node_id FROM instances WHERE id=?", id).Scan(&node))
	require.Equal(t, source, node)
	require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'", id).Scan(&host))
	require.Equal(t, "source-host", host)
	require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.uuid'", id).Scan(&uuidCurrent))
	require.Equal(t, a.InstanceUUID, uuidCurrent)
	require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
	require.NoError(t, tx.RetireOVNNICMigrationPreclaim(ctx, operation, "eth0", claim))
	tx.nodeID = source
	require.NoError(t, tx.AuthorizeOVNNICMigration(ctx, uuid.NewString(), id, a.InstanceUUID, target))
}

func TestOVNNICMigrationHandoverPlacementAndRetirement(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
	source := tx.nodeID
	require.Error(t, tx.OVNNICMigrationHandover(ctx, operation))
	tx.nodeID = target
	claim := migrationTargetClaim(a, id, target)
	require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
	m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
	require.NoError(t, err)
	require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"root":"bound-shared-rows"}`))
	require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"root":"target-ovs"}`, true))
	require.NoError(t, tx.OVNNICMigrationReady(ctx, operation, "eth0"))
	tx.nodeID = source
	require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
	require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
	committed, err := tx.OVNNICMigrationCommitted(ctx, operation)
	require.NoError(t, err)
	require.True(t, committed)
	cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
	require.NoError(t, err)
	require.False(t, cleared)
	require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
	require.NoError(t, tx.EnsureOVNNICMigrationSourceComplete(ctx, operation, a.InstanceUUID))
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
	require.Error(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, source))
	require.NoError(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
	_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, id)
	require.NoError(t, err)
	var host, sibling, keep, identity string
	for key, out := range map[string]*string{"volatile.eth0.host_name": &host, "volatile.sibling.host_name": &sibling, "user.keep": &keep, "volatile.uuid": &identity} {
		require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key=?", id, key).Scan(out))
	}

	require.Equal(t, "target-host", host)
	require.Equal(t, "sibling", sibling)
	require.Equal(t, "unrelated", keep)
	require.Equal(t, a.InstanceUUID, identity)
	// Original immutable source receipt survives and never clears target config.
	cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
	require.False(t, cleared)
	require.Error(t, err)
	tx.nodeID = target
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
}

func TestOVNNICMigrationReplacementAndFailureDebt(t *testing.T) {
	for _, name := range []string{"source-config-race", "instance-uuid-race", "target-reuse", "operation-reuse", "attempted-publication", "ovs-ambiguity"} {
		t.Run(name, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, _, id, target, operation := ovnMigrationFixture(t, tx)
			source := tx.nodeID
			tx.nodeID = target
			claim := migrationTargetClaim(a, id, target)
			require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
			switch name {
			case "source-config-race":
				_, err := tx.tx.ExecContext(ctx, "UPDATE instances_config SET value='foreign-host' WHERE instance_id=? AND key='volatile.eth0.host_name'", id)
				require.NoError(t, err)
			case "instance-uuid-race":
				_, err := tx.tx.ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key='volatile.uuid'", uuid.NewString(), id)
				require.NoError(t, err)
			case "target-reuse":
				tx.nodeID = source
			case "operation-reuse":
				operation = uuid.NewString()
			case "attempted-publication":
				m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
				require.NoError(t, err)
				require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"attempt":"pending"}`))
			case "ovs-ambiguity":
				require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", "", true))
			}

			require.Error(t, tx.RetireOVNNICMigrationPreclaim(ctx, operation, "eth0", claim))
			tx.nodeID = source
			require.Error(t, tx.AuthorizeOVNNICMigration(ctx, uuid.NewString(), id, a.InstanceUUID, target))
			require.True(t, api.StatusErrorCheck(tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID), 409))
		})
	}
}

func TestOVNNICMigrationMemberDebtSurvivesPlacement(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
	source := tx.nodeID
	require.Error(t, tx.RemoveNode(target))
	require.Error(t, tx.RemoveNode(source))
	tx.nodeID = target
	claim := migrationTargetClaim(a, id, target)
	require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
	m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
	require.NoError(t, err)
	require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"bound":"NB"}`))
	require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"bound":"OVS"}`, true))
	require.NoError(t, tx.OVNNICMigrationReady(ctx, operation, "eth0"))
	require.Error(t, tx.AbortEmptyOVNNICMigration(ctx, operation))
	tx.nodeID = source
	require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
	require.Error(t, tx.EnsureOVNNICMigrationPlaced(ctx, operation, id, a.InstanceUUID, target))
	require.NoError(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
	_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, id)
	require.NoError(t, err)
	require.NoError(t, tx.EnsureOVNNICMigrationPlaced(ctx, operation, id, a.InstanceUUID, target))
	require.Error(t, tx.RemoveNode(source)) // Placement is not original source cleanup completion.
	require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
	require.NoError(t, tx.RemoveNode(source))
	var savedSource, savedTarget int64
	require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT source_node_id,target_node_id FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&savedSource, &savedTarget))
	require.Equal(t, source, savedSource)
	require.Equal(t, target, savedTarget)
	_, err = tx.tx.ExecContext(ctx, "INSERT INTO nodes(id,name,description,address,schema,api_extensions,arch) VALUES (?,'replacement-member','','192.0.2.99:8443',85,1,2)", source)
	require.NoError(t, err)
	tx.nodeID = source
	require.Error(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], nil))
}

func TestOVNNICMigrationPositiveEmptyAbortAndLateTargetRefusal(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, _, id, target, operation := ovnMigrationFixture(t, tx)
	require.NoError(t, tx.AbortEmptyOVNNICMigration(ctx, operation))
	require.NoError(t, tx.RemoveNode(target))
	tx.nodeID = target
	require.Error(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], nil))
	require.Error(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(migrationTargetClaim(a, id, target))))
}

// TestOVNNICMigrationPostHandoverUnrelatedVolatile covers a live-migrated target that writes
// ordinary volatile keys after the source handover: they need no staging authority, while a late
// allocation write is still refused.
func TestOVNNICMigrationPostHandoverUnrelatedVolatile(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	a, _, id, target, operation := ovnMigrationFixture(t, tx)
	tx.nodeID = target
	require.NoError(t, tx.EnsureOVNNICMigrationStart(ctx, operation, id, a.InstanceUUID, "eth0", a.NetworkIDs[0], nil))
	_, err := tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='handover' WHERE operation=?", operation)
	require.NoError(t, err)
	require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, map[string]string{"volatile.last_state.power": "RUNNING", "volatile.eth0.hwaddr": "00:16:3e:00:00:01", "volatile.sibling.host_name": "other"}))
	require.ErrorContains(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(migrationTargetClaim(a, id, target))), "lost target authority")
}
