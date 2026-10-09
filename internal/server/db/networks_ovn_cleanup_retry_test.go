//go:build linux && cgo && !agent

package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOVNNICOrdinarySourceHookWithDeferredPrecopy(t *testing.T) {
	for _, mode := range []string{"deferred-copy", "mixed-bridged-host", "copy-hook", "copy-host", "captured-ovn-host", "copy-borrowed-claim", "copy-unlisted-claim", "foreign-uuid", "authorized-stage", "committed-stage", "source-placement-changed", "source-uuid-changed"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, _, id, target, operation := ovnMigrationFixture(t, tx)
			phase := "aborted"
			switch mode {
			case "authorized-stage":
				phase = "authorized"
			case "committed-stage":
				phase = "handover"
			}

			_, err := tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase=? WHERE operation=?", phase, operation)
			require.NoError(t, err)
			res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'deferred-precopy',1,0,'',1)", target)
			require.NoError(t, err)
			copyID, err := res.LastInsertId()
			require.NoError(t, err)
			identity := a.InstanceUUID
			if mode == "foreign-uuid" {
				identity = uuid.NewString()
			}

			_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", copyID, identity)
			require.NoError(t, err)
			if mode == "mixed-bridged-host" {
				for key, value := range map[string]string{"volatile.eth1.host_name": "bridged-original-host", "volatile.eth1.last_state.created": "true", "volatile.eth1.last_state.mtu": "1500"} {
					_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", copyID, key, value)
					require.NoError(t, err)
				}
			}

			if mode == "copy-host" || mode == "captured-ovn-host" || mode == "copy-borrowed-claim" || mode == "copy-unlisted-claim" {
				key, value := "volatile.eth0.host_name", "borrowed-source-host"
				if mode == "copy-borrowed-claim" || mode == "copy-unlisted-claim" {
					key, value = "volatile.eth0.last_state.ovn.host", `{"original":true}`
				}

				if mode == "copy-unlisted-claim" {
					key = "volatile.other.last_state.ovn.host"
				}

				_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", copyID, key, value)
				require.NoError(t, err)
			}

			local := map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.host_name": "source-host", "volatile.eth0.last_state.ovn.host": `{"original":true}`}
			if mode == "captured-ovn-host" {
				delete(local, "volatile.eth0.last_state.ovn.host")
			}

			hookID, hookUUID := id, a.InstanceUUID
			switch mode {
			case "copy-hook":
				hookID = int(copyID)
				tx.nodeID = target
			case "source-placement-changed":
				_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, id)
				require.NoError(t, err)
			case "source-uuid-changed":
				hookUUID = uuid.NewString()
			}

			selected, err := tx.OVNNICMigrationSourceHook(ctx, hookID, hookUUID, local)
			if mode == "deferred-copy" || mode == "mixed-bridged-host" || mode == "foreign-uuid" {
				require.NoError(t, err)
				require.Empty(t, selected, "an ordinary source teardown cannot acquire staged migration authority")
			} else {
				require.Error(t, err)
			}

			var claim, receipt string
			require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", id).Scan(&claim))
			require.Equal(t, `{"original":true}`, claim)
			current, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
			require.NoError(t, err)
			require.Equal(t, a, current, "hook admission cannot change or acknowledge original source cleanup")
			require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT source_terminal FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&receipt))
			require.Empty(t, receipt)
		})
	}
}

func TestOVNNICOrdinaryNonOVNSourceHookWithPrecopy(t *testing.T) {
	tx, cleanup := NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	identity := uuid.NewString()
	ids := []int{}
	for _, name := range []string{"bridged-source", "bridged-copy"} {
		res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,0,'',1)", tx.nodeID, name)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		ids = append(ids, int(id))
		for key, value := range map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": "bridged-host"} {
			_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
			require.NoError(t, err)
		}
	}

	selected, err := tx.OVNNICMigrationSourceHook(ctx, ids[0], identity, map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": "bridged-host"})
	require.NoError(t, err)
	require.Empty(t, selected)
	selected, err = tx.OVNNICMigrationSourceHook(ctx, ids[1], identity, map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": "bridged-host"})
	require.NoError(t, err, "an ordinary non-OVN copy must still record its own stopped state")
	require.Empty(t, selected, "ordinary hook discovery grants no original OVN terminal authority")
	require.Error(t, tx.EnsureOVNNICOriginalInstance(ctx, ids[1], identity))
}

func TestOVNNICMigrationSourceTerminalRetryReceipt(t *testing.T) {
	for _, mode := range []string{"success", "missing-terminal", "pre-handover", "wrong-operation", "wrong-instance", "wrong-uuid", "wrong-member", "target-local", "project-replaced", "instance-payload-replaced", "generation-replaced", "payload-replaced", "shared-replaced", "target-receipt-replaced", "persistence-failure", "new-target-generation", "target-stopped"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
			source := tx.nodeID
			local := map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.host_name": "source-host", "volatile.eth0.last_state.ovn.host": `{"original":true}`}
			if mode == "pre-handover" {
				require.Error(t, tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, id, a.InstanceUUID, local))
				return
			}

			tx.nodeID = target
			claim := migrationTargetClaim(a, id, target)
			require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, migrationStagedChanges(claim)))
			m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
			require.NoError(t, err)
			require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"immutable":"rooted-plan"}`))
			require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"root":"inert-ovs"}`, true))
			require.NoError(t, tx.OVNNICMigrationReady(ctx, operation, "eth0"))
			tx.nodeID = source
			require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
			switch mode {
			case "wrong-operation":
				operation = uuid.NewString()
			case "wrong-instance":
				id++
			case "wrong-uuid":
				a.InstanceUUID = uuid.NewString()
			case "wrong-member":
				tx.nodeID = target
			case "target-local":
				local = migrationStagedChanges(claim)
			case "persistence-failure":
				_, err = tx.tx.ExecContext(ctx, `CREATE TRIGGER refuse_source_terminal BEFORE UPDATE OF source_terminal ON networks_ovn_nic_migrations BEGIN SELECT RAISE(ABORT, 'injected terminal persistence failure'); END`)
				require.NoError(t, err)
			}

			if mode != "missing-terminal" {
				err = tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, id, a.InstanceUUID, local)
				if mode == "wrong-operation" || mode == "wrong-instance" || mode == "wrong-uuid" || mode == "wrong-member" || mode == "target-local" || mode == "persistence-failure" {
					require.Error(t, err)
					return
				}

				require.NoError(t, err)
				m, err = tx.OVNNICMigrationDevice(ctx, operation, "eth0")
				require.NoError(t, err)
				receipt := m.SourceTerminal
				require.NoError(t, tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, id, a.InstanceUUID, local))
				m, err = tx.OVNNICMigrationDevice(ctx, operation, "eth0")
				require.NoError(t, err)
				require.Equal(t, receipt, m.SourceTerminal)
			}

			require.NoError(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
			_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, id)
			require.NoError(t, err)
			switch mode {
			case "project-replaced":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET project_id=project_id+1 WHERE operation=?", operation)
			case "instance-payload-replaced":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET payload=json_set(payload,'$.InstanceID',?) WHERE generation=?", id+1, a.Generation)
			case "generation-replaced":
				replacement := uuid.NewString()
				_, err = tx.tx.ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload,completed) SELECT ?,source_node_id,instance_uuid,device_name,version,network_ids,payload,1 FROM networks_ovn_nic_cleanup WHERE generation=?", replacement, a.Generation)
				require.NoError(t, err)
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET generation=? WHERE operation=? AND device_name=?", replacement, operation, a.DeviceName)
			case "payload-replaced":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET payload=json_set(payload,'$.Replaced',1) WHERE generation=?", a.Generation)
			case "shared-replaced":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET shared_plan='{}' WHERE operation=?", operation)
			case "target-receipt-replaced":
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET target_volatile='{}' WHERE operation=?", operation)
			case "new-target-generation":
				_, err = tx.tx.ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", `{"SourceNodeID":2,"generation":"new-target"}`, id)
			case "target-stopped":
				_, err = tx.tx.ExecContext(ctx, "DELETE FROM instances_config WHERE instance_id=? AND key LIKE 'volatile.eth0.%'", id)
			}

			require.NoError(t, err)
			plans, err := tx.OVNNICCleanupRetries(ctx)
			if mode == "missing-terminal" || mode == "project-replaced" || mode == "instance-payload-replaced" || mode == "generation-replaced" || mode == "payload-replaced" || mode == "shared-replaced" || mode == "target-receipt-replaced" {
				require.Error(t, err)
				pending, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
				require.NoError(t, err)
				require.False(t, pending.Completed)
				return
			}

			require.NoError(t, err)
			require.Len(t, plans, 1)
			require.True(t, plans[0].Transferred)
			require.Equal(t, a.Generation, plans[0].Sources[0].Generation)
			before := map[string]string{}
			rows, err := tx.tx.QueryContext(ctx, "SELECT key,value FROM instances_config WHERE instance_id=?", id)
			require.NoError(t, err)
			for rows.Next() {
				var key, value string
				require.NoError(t, rows.Scan(&key, &value))
				before[key] = value
			}

			require.NoError(t, rows.Close())
			cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
			require.NoError(t, err)
			require.False(t, cleared)
			require.NoError(t, tx.EnsureOVNNICCleanupRetired(ctx, a))
			require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
			require.NoError(t, tx.EnsureOVNNICMigrationSourceComplete(ctx, operation, a.InstanceUUID))
			for key, value := range before {
				var after string
				require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key=?", id, key).Scan(&after))
				require.Equal(t, value, after)
			}
		})
	}
}

func TestOVNNICMigrationTerminalReceiptCanonicalDeviceOrder(t *testing.T) {
	first := OVNNICMigration{Operation: uuid.NewString(), InstanceID: 1, ProjectID: 1, InstanceUUID: uuid.NewString(), SourceNodeID: 1, TargetNodeID: 2, DeviceName: "a", Source: OVNNICCleanup{Generation: uuid.NewString(), Payload: "{}"}, SharedPlan: "{}", TargetVolatile: map[string]string{"b": "2", "a": "1"}}
	other := first
	other.DeviceName = "z"
	makeReceipt := func(order []OVNNICMigration) string {
		r := ovnNICSourceTerminal{Operation: first.Operation, InstanceID: first.InstanceID, ProjectID: first.ProjectID, InstanceUUID: first.InstanceUUID, SourceNodeID: first.SourceNodeID, TargetNodeID: first.TargetNodeID, Devices: map[string]ovnNICSourceTerminalDevice{}}
		for _, m := range order {
			r.Devices[m.DeviceName] = m.sourceTerminalDevice()
		}

		wire, err := json.Marshal(r)
		require.NoError(t, err)
		return string(wire)
	}

	require.Equal(t, makeReceipt([]OVNNICMigration{first, other}), makeReceipt([]OVNNICMigration{other, first}))
}

func TestOVNNICMigrationSourceTerminalCompletedSiblingReplacement(t *testing.T) {
	for _, mode := range []string{"control", "changed-completed-child", "removed-completed-child"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, tokens, id, target, operation := ovnMigrationFixture(t, tx)
			source := tx.nodeID
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(a.Payload), &payload))
			host := map[string]string{"host_name": "source-second"}
			payload["HostVolatile"] = host
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			second := a
			second.Generation = uuid.NewString()
			second.DeviceName = "eth1"
			second.Payload = string(raw)
			_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.eth1.host_name','source-second')", id)
			require.NoError(t, err)
			require.NoError(t, tx.CaptureOVNNICCleanup(ctx, second, tokens))
			require.NoError(t, tx.AuthorizeOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
			tx.nodeID = target
			for _, original := range []OVNNICCleanup{a, second} {
				claim := migrationTargetClaim(original, id, target)
				changes := map[string]string{}
				for key, value := range claim {
					changes["volatile."+original.DeviceName+"."+key] = value
				}

				require.NoError(t, tx.SetOVNNICMigrationVolatile(ctx, operation, id, a.InstanceUUID, changes))
				m, err := tx.OVNNICMigrationDevice(ctx, operation, original.DeviceName)
				require.NoError(t, err)
				require.NoError(t, tx.SetOVNNICMigrationShared(ctx, m, `{"root":"second-stage"}`))
				require.NoError(t, tx.SetOVNNICMigrationOVS(ctx, operation, original.DeviceName, `{"root":"inert-ovs"}`, true))
				require.NoError(t, tx.OVNNICMigrationReady(ctx, operation, original.DeviceName))
			}

			tx.nodeID = source
			require.NoError(t, tx.OVNNICMigrationHandover(ctx, operation))
			local := map[string]string{"volatile.eth0.host_name": "source-host", "volatile.eth0.last_state.ovn.host": `{"original":true}`, "volatile.eth1.host_name": "source-second"}
			require.NoError(t, tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, id, a.InstanceUUID, local))
			cleared, err := tx.RetireOVNNICCleanupVolatile(ctx, second, tokens)
			require.NoError(t, err)
			require.False(t, cleared)
			require.NoError(t, tx.CompleteOVNNICCleanup(ctx, second, tokens))
			require.NoError(t, tx.PlaceOVNNICMigration(ctx, operation, id, a.InstanceUUID, target))
			_, err = tx.tx.ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, id)
			require.NoError(t, err)
			if mode == "changed-completed-child" {
				_, err = tx.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET shared_plan='{}' WHERE operation=? AND device_name='eth1'", operation)
				require.NoError(t, err)
			}

			if mode == "removed-completed-child" {
				_, err = tx.tx.ExecContext(ctx, "DELETE FROM networks_ovn_nic_migration_devices WHERE operation=? AND device_name='eth1'", operation)
				require.NoError(t, err)
			}

			plans, err := tx.OVNNICCleanupRetries(ctx)
			switch mode {
			case "control":
				require.NoError(t, err)
				require.Len(t, plans, 1)
				require.Len(t, plans[0].Sources, 1)
			default:
				require.Error(t, err)
				require.Nil(t, plans)
			}

			pending, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
			require.NoError(t, err)
			require.False(t, pending.Completed)
		})
	}
}
