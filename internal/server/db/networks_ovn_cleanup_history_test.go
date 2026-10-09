//go:build linux && cgo && !agent

package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVNNICCompletedPlacedHistoryDoesNotBlockOrdinarySource(t *testing.T) {
	for _, mode := range []string{"complete", "pending-cleanup", "missing-retirement", "missing-terminal", "changed-terminal-tuple", "changed-device-set", "changed-shared-plan", "changed-payload-instance", "authorized", "handover"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
			source := tx.nodeID
			tx.nodeID = target
			claim := migrationTargetClaim(a, id, target)
			require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
			m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
			require.NoError(t, err)
			require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"immutable":"shared-plan"}`))
			require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"immutable":"OVS-plan"}`, true))
			require.NoError(t, tx.OVNNICMigrationReady(ctx, operation, "eth0"))
			tx.nodeID = source
			require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
			require.NoError(t, tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, id, a.InstanceUUID, map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.host_name": "source-host", "volatile.eth0.last_state.ovn.host": `{"original":true}`}))
			cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
			require.NoError(t, err)
			require.False(t, cleared)
			require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
			require.NoError(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
			// The current record has returned cold to its original member with no current host allocation.
			_, err = tx.tx.ExecContext(ctx, "DELETE FROM instances_config WHERE instance_id=? AND key GLOB 'volatile.eth0.*'", id)
			require.NoError(t, err)
			res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'new-precopy',1,0,'',1)", target)
			require.NoError(t, err)
			copyID, err := res.LastInsertId()
			require.NoError(t, err)
			_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", copyID, a.InstanceUUID)
			require.NoError(t, err)
			switch mode {
			case "pending-cleanup":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET completed=0 WHERE generation=?", a.Generation)
			case "missing-retirement":
				_, err = tx.tx.ExecContext(ctx, "DELETE FROM networks_ovn_nic_cleanup_retirement WHERE generation=?", a.Generation)
			case "missing-terminal":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_terminal='' WHERE operation=?", operation)
			case "changed-terminal-tuple", "changed-device-set":
				m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
				require.NoError(t, err)
				var terminal ovnNICSourceTerminal
				require.NoError(t, json.Unmarshal([]byte(m.SourceTerminal), &terminal))
				if mode == "changed-terminal-tuple" {
					terminal.InstanceID++
				} else {
					terminal.Devices["extra"] = terminal.Devices["eth0"]
				}

				raw, err := json.Marshal(terminal)
				require.NoError(t, err)
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_terminal=? WHERE operation=?", string(raw), operation)
				require.NoError(t, err)
			case "changed-shared-plan":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET shared_plan='{}' WHERE operation=?", operation)
			case "changed-payload-instance":
				var payload map[string]any
				require.NoError(t, json.Unmarshal([]byte(a.Payload), &payload))
				payload["InstanceID"] = id + 1
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET payload=? WHERE generation=?", string(raw), a.Generation)
				require.NoError(t, err)
			case "authorized", "handover":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase=? WHERE operation=?", mode, operation)
			}

			require.NoError(t, err)
			err = tx.EnsureOVNNICOriginalInstance(ctx, id, a.InstanceUUID)
			selected, hookErr := tx.OVNNICMigrationSourceHook(ctx, id, a.InstanceUUID, map[string]string{"volatile.uuid": a.InstanceUUID})
			if mode == "complete" {
				require.NoError(t, err)
				require.NoError(t, hookErr)
				require.Empty(t, selected, "completed history must not grant a new migration authority")
			} else {
				require.Error(t, err)
				require.Error(t, hookErr)
			}
		})
	}
}
