//go:build linux && cgo && !agent

package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVNNICOrdinaryPrecopyRetirementAndTerminal(t *testing.T) {
	for _, mode := range []string{"deferred", "repeated", "rollback", "copy-host", "copy-claim", "unlisted-claim", "authorized-stage", "replaced-source-host"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
			if mode != "authorized-stage" {
				_, err := tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='aborted' WHERE operation=?", operation)
				require.NoError(t, err)
			}

			res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'deferred-precopy',1,0,'',1)", target)
			require.NoError(t, err)
			copyID, err := res.LastInsertId()
			require.NoError(t, err)
			copyConfig := map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth1.host_name": "unrelated-bridged-host", "user.keep": "copy-config"}
			switch mode {
			case "copy-host":
				copyConfig["volatile.eth0.host_name"] = "borrowed-source-host"
			case "copy-claim":
				copyConfig["volatile.eth0.last_state.ovn.host"] = `{"original":true}`
			case "unlisted-claim":
				copyConfig["volatile.other.last_state.ovn.physical"] = `{"original":true}`
			}

			for key, value := range copyConfig {
				_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", copyID, key, value)
				require.NoError(t, err)
			}

			switch mode {
			case "rollback":
				_, err = tx.tx.ExecContext(ctx, "DELETE FROM instances WHERE id=?", copyID)
				require.NoError(t, err)
			case "replaced-source-host":
				_, err = tx.tx.ExecContext(ctx, "UPDATE instances_config SET value='replacement-host' WHERE instance_id=? AND key='volatile.eth0.host_name'", id)
				require.NoError(t, err)
			}

			ordinary := mode == "deferred" || mode == "repeated" || mode == "rollback" || mode == "replaced-source-host"
			err = tx.EnsureOVNNICOriginalInstance(ctx, id, a.InstanceUUID)
			if ordinary {
				require.NoError(t, err, "the source effect fence admits only the original with inert newer targets")
				plans, err := tx.OVNNICCleanupRetries(ctx)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				require.Equal(t, id, plans[0].InstanceID)
			} else {
				require.Error(t, err)
			}

			require.Error(t, tx.EnsureOVNNICCleanupRetired(ctx, a), "terminal teardown cannot create the successful host-hook receipt")
			cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
			success := mode == "deferred" || mode == "repeated" || mode == "rollback"
			if success {
				require.NoError(t, err)
				require.True(t, cleared)
				require.NoError(t, tx.EnsureOVNNICCleanupRetired(ctx, a))
				if mode == "repeated" {
					cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
					require.NoError(t, err)
					require.True(t, cleared)
				}
			} else {
				require.Error(t, err)
				require.False(t, cleared)
				require.Error(t, tx.EnsureOVNNICCleanupRetired(ctx, a))
			}

			pending, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
			require.NoError(t, err)
			require.Equal(t, a, pending, "retirement must preserve the immutable source attempt and pending terminal acknowledgment")
			require.Error(t, tx.EnsureOVNNICTransferSource(ctx, a.InstanceUUID))
			var host, claim, sibling, identity string
			require.NoError(t, tx.tx.QueryRowContext(ctx, `SELECT
				COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'),''),
				COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'),''),
				(SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.sibling.host_name'),
				(SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.uuid')`, id, id, id, id).Scan(&host, &claim, &sibling, &identity))
			require.Equal(t, a.InstanceUUID, identity)
			require.Equal(t, "sibling", sibling)
			if success {
				require.Empty(t, host)
				require.Empty(t, claim)
				require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
				require.NoError(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
				require.NoError(t, tx.EnsureOVNNICTransferSource(ctx, a.InstanceUUID))
				selected, err := tx.OVNNICMigrationSourceHook(ctx, id, a.InstanceUUID, nil)
				require.NoError(t, err)
				require.Empty(t, selected)
				claim, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": a.NetworkIDs[0], "SourceNodeID": tx.nodeID, "InstanceID": id, "InstanceUUID": a.InstanceUUID, "DeviceName": "eth0"})
				require.NoError(t, err)
				_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.eth0.last_state.ovn.host',?)", id, string(claim))
				require.NoError(t, err)
				require.NoError(t, tx.EnsureOVNNICCleanupStart(ctx, a.InstanceUUID, "eth0", a.NetworkIDs[0], map[string]string{"last_state.ovn.host": string(claim)}), "rollback source publication must pass while the inert target still exists")
				if mode != "rollback" {
					require.Error(t, tx.EnsureOVNNICOriginalInstance(ctx, int(copyID), a.InstanceUUID), "the target cannot publish the original identity")
				}
			} else {
				wantHost := "source-host"
				if mode == "replaced-source-host" {
					wantHost = "replacement-host"
				}

				require.Equal(t, wantHost, host)
				require.Equal(t, `{"original":true}`, claim)
			}

			if mode != "rollback" {
				for key, value := range copyConfig {
					var current string
					require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key=?", copyID, key).Scan(&current))
					require.Equal(t, value, current, "original retirement must never borrow or clear the target's metadata")
				}
			}

			var receipt string
			require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT source_terminal FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&receipt))
			require.Empty(t, receipt, "ordinary retirement cannot manufacture staged source authority")
		})
	}
}

func TestOVNNICSourceHookOrdinaryPreservedCopy(t *testing.T) {
	for _, mode := range []string{"ordinary", "original-host-claim", "copied-physical-claim", "moved-copy"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			identity := "85e669ec-0ab4-46a5-8d15-ff39c63a5eb4"
			ids := []int{}
			for _, name := range []string{"original", "preserved-copy"} {
				res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?, ?, 1,0,'',1)", tx.nodeID, name)
				require.NoError(t, err)
				id, err := res.LastInsertId()
				require.NoError(t, err)
				ids = append(ids, int(id))
				for key, value := range map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": name + "-bridged-host"} {
					_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
					require.NoError(t, err)
				}
			}
			switch mode {
			case "original-host-claim", "copied-physical-claim":
				id, key := ids[0], "volatile.eth0.last_state.ovn.host"
				if mode == "copied-physical-claim" {
					id, key = ids[1], "volatile.eth0.last_state.ovn.physical"
				}

				_, err := tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, `{"borrowed":true}`)
				require.NoError(t, err)
			case "moved-copy":
				node, err := tx.CreateNode("foreign-member", "192.0.2.100:8443")
				require.NoError(t, err)
				_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", node, ids[1])
				require.NoError(t, err)
			}

			selected, err := tx.OVNNICMigrationSourceHook(ctx, ids[1], identity, nil)
			if mode == "ordinary" {
				require.NoError(t, err)
				require.Empty(t, selected)
			} else {
				require.Error(t, err)
			}

			require.Error(t, tx.EnsureOVNNICOriginalInstance(ctx, ids[1], identity), "hook discovery must never authorize the copy to publish the original OVN identity")
		})
	}
}
