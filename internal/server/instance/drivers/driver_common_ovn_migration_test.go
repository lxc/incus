package drivers

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/migration"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/state"
)

func driverMigrationFixture(t *testing.T) (*common, string, map[string]string) {
	t.Helper()
	cluster, closeCluster := db.NewTestCluster(t)
	t.Cleanup(closeCluster)
	identity, operation := uuid.NewString(), uuid.NewString()
	original := map[string]string{"volatile.uuid": identity, "volatile.unrelated.host_name": "keep", "user.keep": "unchanged"}
	var id int
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		source, err := tx.CreateNode("source-original", "192.0.2.40:8443")
		if err != nil {
			return err
		}

		result, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'moving',1,0,'',1)", source)
		if err != nil {
			return err
		}

		iid, err := result.LastInsertId()
		if err != nil {
			return err
		}

		id = int(iid)
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) VALUES (?,1,?,?,?,?,'authorized')", operation, id, identity, source, cluster.GetNodeID())
		if err != nil {
			return err
		}

		for _, name := range []string{"eth0", "eth1"} {
			host, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "InstanceID": id, "InstanceUUID": identity, "SourceNodeID": source, "DeviceName": name, "Alias": "source-" + name})
			if err != nil {
				return err
			}

			values := map[string]string{"host_name": "source-" + name, "last_state.ovn.host": string(host)}
			for key, v := range values {
				original["volatile."+name+"."+key] = v
			}

			payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "HostVolatile": values})
			if err != nil {
				return err
			}

			gen := uuid.NewString()
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload) VALUES (?,?,?,?,1,'[41]',?)", gen, source, identity, name, string(payload))
			if err != nil {
				return err
			}

			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_devices(operation,device_name,generation) VALUES (?,?,?)", operation, name, gen)
			if err != nil {
				return err
			}
		}

		for key, value := range original {
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
			if err != nil {
				return err
			}
		}

		return nil
	}))
	return &common{id: id, localConfig: maps.Clone(original), expandedConfig: maps.Clone(original), state: &state.State{DB: &db.DB{Cluster: cluster}}}, operation, original
}

func TestDriverOVNNICMigrationTargetActualVolatileAndPlacement(t *testing.T) {
	for _, kind := range []string{"lxc", "qemu"} {
		t.Run(kind, func(t *testing.T) {
			d, op, original := driverMigrationFixture(t)
			sourceMap := d.localConfig
			var target interface {
				OVNNICMigrationTarget(string) error
				VolatileSet(map[string]string) error
				OVNNICMigrationRetire(string, map[string]string) error
				OVNNICMigrationRefresh() error
			}

			if kind == "lxc" {
				target = &lxc{common: *d}
			} else {
				target = &qemu{common: *d}
			}

			require.NoError(t, target.OVNNICMigrationTarget(op))
			require.Equal(t, original, sourceMap)
			claims := map[string]map[string]string{}
			for _, name := range []string{"eth0", "eth1"} {
				raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "InstanceID": d.id, "InstanceUUID": original["volatile.uuid"], "SourceNodeID": d.state.DB.Cluster.GetNodeID(), "DeviceName": name, "Alias": "target-" + name})
				require.NoError(t, err)
				claim := map[string]string{"host_name": "target-" + name, "last_state.ovn.host": string(raw)}
				claims[name] = claim
				changes := map[string]string{"volatile." + name + ".hwaddr": "00:16:3e:12:34:56", "volatile.last_state.power": "RUNNING"}
				for key, v := range claim {
					changes["volatile."+name+"."+key] = v
				}

				require.NoError(t, target.VolatileSet(changes))
			}
			// First and second claims persist only in their exact device stage, including under QEMU's old disable mode.
			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				for _, name := range []string{"eth0", "eth1"} {
					m, err := tx.OVNNICMigrationDevice(ctx, op, name)
					if err != nil {
						return err
					}

					require.Equal(t, claims[name], m.TargetVolatile)
					require.Error(t, tx.EnsureOVNNICMigrationStart(ctx, op, d.id, original["volatile.uuid"], name, 41, nil))
				}

				values, err := tx.Tx().QueryContext(ctx, "SELECT key,value FROM instances_config WHERE instance_id=?", d.id)
				if err != nil {
					return err
				}

				defer func() { require.NoError(t, values.Close()) }()
				stored := map[string]string{}
				for values.Next() {
					var k, v string
					if err = values.Scan(&k, &v); err != nil {
						return err
					}

					stored[k] = v
				}

				require.Equal(t, original, stored)
				return values.Err()
			}))
			require.Error(t, target.OVNNICMigrationTarget(op))
			wrong := maps.Clone(claims["eth0"])
			wrong["host_name"] = "foreign"
			require.Error(t, target.OVNNICMigrationRetire("eth0", wrong))
			require.NoError(t, target.OVNNICMigrationRetire("eth0", claims["eth0"]))
			changes := map[string]string{}
			for key, v := range claims["eth0"] {
				changes["volatile.eth0."+key] = v
			}

			require.NoError(t, target.VolatileSet(changes))
			// Missing/uncertain placement never changes VolatileSet to ordinary SQL persistence.
			require.NoError(t, target.OVNNICMigrationRefresh())
			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='placed' WHERE operation=?", op)
				return err
			}))
			require.Error(t, target.OVNNICMigrationRefresh()) // phase alone with original placement is not an ACK.
		})
	}
}

func TestDriverOVNNICMigrationReceiveRequiresActualTargetStage(t *testing.T) {
	args := instance.MigrateReceiveArgs{MigrateArgs: instance.MigrateArgs{NICMigrationOperation: uuid.NewString()}}
	require.ErrorContains(t, (&lxc{}).MigrateReceive(args), "operation binding changed")
	require.ErrorContains(t, (&qemu{}).MigrateReceive(args), "operation binding changed")
}

func TestDriverOVNNICMigrationSourceMissingOutcomeRefuses(t *testing.T) {
	d, _, _ := driverMigrationFixture(t)
	d.ovnMigrationSource = uuid.NewString()
	require.Error(t, d.commitOVNNICMigrationHandover())
	require.Error(t, d.abortEmptyOVNNICMigration())
}

func TestDriverOVNNICMigrationInheritedPersistDisable(t *testing.T) {
	d, _, original := driverMigrationFixture(t)
	d.volatileSetPersistDisable = true
	require.NoError(t, d.VolatileSet(map[string]string{"volatile.eth0.host_name": "memory-only-original-stop"}))
	require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var value string
		err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'", d.id).Scan(&value)
		require.Equal(t, original["volatile.eth0.host_name"], value)
		return err
	}))
}

func TestDriverOVNNICMigrationDefaultQEMULiveNegotiation(t *testing.T) {
	for _, name := range []string{"same85-live", "missing-live-state", "legacy-criu-state", "stopped-ordinary"} {
		t.Run(name, func(t *testing.T) {
			live := name != "stopped-ordinary"
			offer := migration.MigrationHeader{Criu: migration.CRIUType_VM_QEMU.Enum()}
			response := migration.MigrationHeader{}
			if name == "legacy-criu-state" {
				offer.Criu = migration.CRIUType_CRIU_RSYNC.Enum()
			}

			if name == "missing-live-state" {
				offer.Criu = nil
			}

			if qemuMigrationUsesLiveState(live, offer.Criu) {
				response.Criu = migration.CRIUType_VM_QEMU.Enum()
			}

			supported := qemuMigrationUsesLiveState(live, response.Criu)
			if name == "same85-live" {
				require.True(t, supported)
				require.NoError(t, requireStagedQEMULiveState("bound-original-operation", supported))
			} else {
				require.False(t, supported)
				if live {
					require.Error(t, requireStagedQEMULiveState("bound-original-operation", supported))
				}
			}

			require.NoError(t, requireStagedQEMULiveState("", supported))
		})
	}
}

func driverSourceHookFixture(t *testing.T) (*common, string, map[string]string) {
	d, operation, original := driverMigrationFixture(t)
	require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var source int64
		err := tx.Tx().QueryRowContext(ctx, "SELECT source_node_id FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&source)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_node_id=?,target_node_id=?,phase='handover' WHERE operation=?", d.state.DB.Cluster.GetNodeID(), source, operation)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", d.state.DB.Cluster.GetNodeID(), d.id)
		if err != nil {
			return err
		}

		for _, name := range []string{"eth0", "eth1"} {
			var host map[string]any
			err = json.Unmarshal([]byte(original["volatile."+name+".last_state.ovn.host"]), &host)
			if err != nil {
				return err
			}

			host["SourceNodeID"] = d.state.DB.Cluster.GetNodeID()
			raw, err := json.Marshal(host)
			if err != nil {
				return err
			}

			original["volatile."+name+".last_state.ovn.host"] = string(raw)
			_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key=?", string(raw), d.id, "volatile."+name+".last_state.ovn.host")
			if err != nil {
				return err
			}

			values := map[string]string{"host_name": original["volatile."+name+".host_name"], "last_state.ovn.host": string(raw)}
			payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": d.id, "HostVolatile": values})
			if err != nil {
				return err
			}

			_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET source_node_id=?,payload=? WHERE instance_uuid=? AND device_name=?", d.state.DB.Cluster.GetNodeID(), string(payload), original["volatile.uuid"], name)
			if err != nil {
				return err
			}

			_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET ready=1,target_volatile='{\"host_name\":\"target-owned\"}',shared_plan='{\"bound\":\"publication\"}' WHERE operation=? AND device_name=?", operation, name)
			if err != nil {
				return err
			}
		}

		return tx.UpdateInstanceConfig(d.id, map[string]string{"volatile.last_state.power": "RUNNING", "volatile.last_state.ready": "true"})
	}))
	d.localConfig = maps.Clone(original)
	d.expandedConfig = maps.Clone(original)
	return d, operation, original
}

func TestDriverOVNNICMigrationFreshSourceHookTerminalAndPower(t *testing.T) {
	for _, name := range []string{"positive-handover", "positive-placed", "placed-fresh-target-config", "placed-later-target-generation", "nonlocal-without-stage", "placed-target-local-hook", "pre-handover", "target-hook", "missing-known-operation", "changed-original-claim", "changed-uuid", "changed-project", "ambiguous-placed", "ordinary"} {
		t.Run(name, func(t *testing.T) {
			d, op, original := driverSourceHookFixture(t)
			require.Empty(t, d.ovnMigrationSource)
			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				switch name {
				case "positive-placed", "placed-fresh-target-config", "placed-later-target-generation", "nonlocal-without-stage", "placed-target-local-hook":
					var target int64
					err := tx.Tx().QueryRowContext(ctx, "SELECT target_node_id FROM networks_ovn_nic_migrations WHERE operation=?", op).Scan(&target)
					if err != nil {
						return err
					}

					placement := target
					if name == "placed-target-local-hook" {
						placement = d.state.DB.Cluster.GetNodeID()
						_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_node_id=target_node_id,target_node_id=? WHERE operation=?", placement, op)
						if err != nil {
							return err
						}
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", placement, d.id)
					if err != nil {
						return err
					}

					phase := "placed"
					if name == "nonlocal-without-stage" {
						phase = "aborted"
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase=? WHERE operation=?", phase, op)
					if err != nil {
						return err
					}

					for _, dev := range []string{"eth0", "eth1"} {
						values := map[string]string{}
						for _, key := range db.OVNNICStopVolatileKeys() {
							values["volatile."+dev+"."+key] = ""
						}

						values["volatile."+dev+".host_name"] = "target-owned"
						if name == "placed-later-target-generation" {
							values["volatile."+dev+".host_name"] = "target-restarted-new-generation"
						}

						err = tx.UpdateInstanceConfig(d.id, values)
						if err != nil {
							return err
						}

						if name == "placed-fresh-target-config" || name == "placed-later-target-generation" || name == "placed-target-local-hook" {
							maps.Copy(d.localConfig, values)
							maps.Copy(d.expandedConfig, values)
						}
					}

				case "pre-handover":
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='authorized' WHERE operation=?", op)
					return err
				case "target-hook":
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_node_id=target_node_id,target_node_id=? WHERE operation=?", d.state.DB.Cluster.GetNodeID(), op)
					return err
				case "missing-known-operation":
					d.ovnMigrationSource = uuid.NewString()
				case "changed-original-claim":
					d.localConfig["volatile.eth0.host_name"] = "foreign-allocation"
				case "changed-uuid":
					return tx.UpdateInstanceConfig(d.id, map[string]string{"volatile.uuid": uuid.NewString()})
				case "changed-project":
					_, err := tx.Tx().ExecContext(ctx, "INSERT INTO projects(id,name,description) VALUES (42,'changed','')")
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET project_id=42 WHERE id=?", d.id)
					return err
				case "ambiguous-placed":
					var target int64
					err := tx.Tx().QueryRowContext(ctx, "SELECT target_node_id FROM networks_ovn_nic_migrations WHERE operation=?", op).Scan(&target)
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, d.id)
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='placed' WHERE operation=?", op)
					if err != nil {
						return err
					}

					duplicate := uuid.NewString()
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) SELECT ?,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase FROM networks_ovn_nic_migrations WHERE operation=?", duplicate, op)
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_devices SELECT ?,device_name,generation,target_volatile,shared_plan,target_ovs,ovs_attempted,ready FROM networks_ovn_nic_migration_devices WHERE operation=?", duplicate, op)
					return err
				case "ordinary":
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='aborted' WHERE operation=?", op)
					return err
				}

				return nil
			}))
			err := d.recordOVNNICSourceHookStopped()
			strict := name == "placed-fresh-target-config" || name == "placed-later-target-generation" || name == "nonlocal-without-stage" || name == "missing-known-operation" || name == "changed-original-claim" || name == "changed-uuid" || name == "changed-project" || name == "ambiguous-placed"
			if strict {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, "STOPPED", d.localConfig["volatile.last_state.power"])
			require.Equal(t, "false", d.localConfig["volatile.last_state.ready"])
			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var power, ready string
				err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.last_state.power'", d.id).Scan(&power)
				if err != nil {
					return err
				}

				err = tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.last_state.ready'", d.id).Scan(&ready)
				if err != nil {
					return err
				}

				if strict || name == "positive-handover" || name == "positive-placed" {
					require.Equal(t, "RUNNING", power)
					require.Equal(t, "true", ready)
				} else {
					require.Equal(t, "STOPPED", power)
					require.Equal(t, "false", ready)
				}

				return nil
			}))
			if name != "positive-handover" && name != "positive-placed" {
				return
			}

			require.Error(t, d.ensureOVNNICSourceHookCleanupComplete()) // Positive transfer alone is not host cleanup.
			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET completed=1 WHERE instance_uuid=?", original["volatile.uuid"])
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup_retirement SELECT generation,0 FROM networks_ovn_nic_cleanup WHERE instance_uuid=?", original["volatile.uuid"])
				return err
			}))
			for _, dev := range []string{"eth0", "eth1"} {
				for _, key := range db.OVNNICStopVolatileKeys() {
					delete(d.localConfig, "volatile."+dev+"."+key)
					delete(d.expandedConfig, "volatile."+dev+"."+key)
				}
			}

			require.NoError(t, d.ensureOVNNICSourceHookCleanupComplete())
			if name == "positive-handover" {
				require.Error(t, d.ensureOVNStopCleanupComplete()) // Preparation still requires placement completion.
			}

			require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var value string
				err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'", d.id).Scan(&value)
				if name == "positive-placed" {
					require.Equal(t, "target-owned", value) // Source terminal ACK never clears target allocation.
				} else {
					require.Equal(t, original["volatile.eth0.host_name"], value)
				}

				return err
			}))
		})
	}
}

func TestDriverOVNNICMigrationLXCDelayedDumpAdmission(t *testing.T) {
	for _, name := range []string{"modern-staged", "legacy-staged", "legacy-ordinary"} {
		t.Run(name, func(t *testing.T) {
			operation := "durably-staged-source-operation"
			if name == "legacy-ordinary" {
				operation = ""
			}

			effects := 0
			startDump := func() error {
				err := requireStagedLXCDelayedHandover(operation, name == "modern-staged")
				if err != nil {
					return err
				}

				effects++
				return nil
			}

			err := startDump()
			if name == "legacy-staged" {
				require.Error(t, err)
				require.Zero(t, effects)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, effects)
			}
		})
	}
}
