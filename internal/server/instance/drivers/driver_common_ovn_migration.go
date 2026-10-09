package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"

	liblxc "github.com/lxc/go-lxc"

	"github.com/lxc/incus/v7/internal/migration"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
)

// OVNNICMigrationSource binds the actual websocket operation before target dispatch.
func (d *lxc) OVNNICMigrationSource(operation string, target int64) error {
	err := d.authorizeOVNNICMigration(d, operation, target)
	if err != nil {
		return err
	}

	err = requireStagedLXCDelayedHandover(d.ovnMigrationSource, liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 0, 4))
	if err != nil {
		return errors.Join(err, d.abortEmptyOVNNICMigration())
	}

	return nil
}

// OVNNICMigrationSource authorizes the original QEMU migration source.
func (d *qemu) OVNNICMigrationSource(operation string, target int64) error {
	return d.authorizeOVNNICMigration(d, operation, target)
}

func (d *common) authorizeOVNNICMigration(inst instance.Instance, operation string, target int64) error {
	err := d.captureOVNStopSource(inst)
	if err != nil {
		return err
	}

	selected := false
	err = d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.AuthorizeOVNNICMigration(ctx, operation, d.id, d.localConfig["volatile.uuid"], target)
		if err != nil {
			return err
		}

		return tx.Tx().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migrations WHERE operation=? AND phase='authorized' AND instance_id=? AND instance_uuid=? AND source_node_id=? AND target_node_id=?)", operation, d.id, d.localConfig["volatile.uuid"], d.state.DB.Cluster.GetNodeID(), target).Scan(&selected)
	})
	if err == nil {
		d.ovnMigrationSource = ""
		if selected {
			d.ovnMigrationSource = operation
		}
	}

	return err
}

// OVNNICMigrationTarget normalizes only exact authorized original NIC allocations locally.
func (d *common) OVNNICMigrationTarget(operation string) error {
	var devices []string
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		rows, err := tx.Tx().QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
		if err != nil {
			return err
		}

		for rows.Next() {
			var name string
			err = rows.Scan(&name)
			if err != nil {
				break
			}

			devices = append(devices, name)
		}

		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}

		for _, name := range devices {
			err = tx.EnsureOVNNICMigrationStart(ctx, operation, d.id, d.localConfig["volatile.uuid"], name, 0, nil)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	selected := map[string]bool{}
	for _, name := range devices {
		selected[name] = true
	}

	for key, value := range d.localConfig {
		if value == "" || !strings.HasPrefix(key, "volatile.") {
			continue
		}

		for _, suffix := range []string{".last_state.ovn.host", ".last_state.ovn.physical"} {
			if strings.HasSuffix(key, suffix) {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "volatile."), suffix)
				if !selected[name] {
					return errors.New("Original OVN allocation has no migration authorization")
				}
			}
		}
	}

	if len(devices) == 0 {
		return nil
	}

	d.localConfig = maps.Clone(d.localConfig)
	d.expandedConfig = maps.Clone(d.expandedConfig)
	for _, name := range devices {
		for _, key := range db.OVNNICStopVolatileKeys() {
			delete(d.localConfig, "volatile."+name+"."+key)
			delete(d.expandedConfig, "volatile."+name+"."+key)
		}
	}

	d.ovnMigrationTarget = operation
	return nil
}

// OVNNICMigrationSourceOperation returns the authorized source operation.
func (d *common) OVNNICMigrationSourceOperation() string { return d.ovnMigrationSource }

// OVNNICMigrationOperation returns the staged target operation.
func (d *common) OVNNICMigrationOperation() string { return d.ovnMigrationTarget }

// OVNNICMigrationCanRollback reports whether target rollback remains available.
func (d *common) OVNNICMigrationCanRollback() bool { return !d.ovnMigrationRestoreAttempted }

// OVNNICMigrationRetire retires the exact original device preclaim.
func (d *common) OVNNICMigrationRetire(deviceName string, original map[string]string) error {
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RetireOVNNICMigrationPreclaim(ctx, d.ovnMigrationTarget, deviceName, original)
	})
	if err != nil {
		return err
	}

	return d.OVNStopCleanupVolatileRetired(d.localConfig["volatile.uuid"], deviceName, original)
}

// OVNNICMigrationOVS loads the original source device OVS capture.
func (d *common) OVNNICMigrationOVS(deviceName string, plan *ovs.NICPortCleanup, attempted bool) error {
	raw := ""
	if plan != nil {
		data, err := json.Marshal(plan)
		if err != nil {
			return err
		}

		raw = string(data)
	}

	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.SetOVNNICMigrationOVS(ctx, d.ovnMigrationTarget, deviceName, raw, attempted)
	})
}

func (d *common) commitOVNNICMigrationHandover() error {
	if d.ovnMigrationSource == "" {
		return nil
	}

	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.OVNNICMigrationHandover(ctx, d.ovnMigrationSource)
	})
	if err != nil {
		readErr := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			committed, err := tx.OVNNICMigrationCommitted(ctx, d.ovnMigrationSource)
			if err != nil {
				return err
			}

			if !committed {
				return errors.New("Migration handover remains unacknowledged")
			}

			return nil
		})
		if readErr != nil {
			return errors.Join(err, readErr)
		}
	}

	return nil
}

// OVNNICMigrationRefresh ends staging only after exact target placement is observed.
func (d *common) OVNNICMigrationRefresh() error {
	if d.ovnMigrationTarget == "" {
		return nil
	}

	var phase string
	var node int64
	var identity string
	var target int64
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, `SELECT m.phase,m.target_node_id,i.node_id,COALESCE(v.value,'') FROM networks_ovn_nic_migrations m JOIN instances i ON i.id=m.instance_id LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE m.operation=? AND m.instance_id=? AND m.instance_uuid=?`, d.ovnMigrationTarget, d.id, d.localConfig["volatile.uuid"]).Scan(&phase, &target, &node, &identity)
	})
	if err != nil {
		return err
	}

	if phase == "placed" {
		if target != d.state.DB.Cluster.GetNodeID() || node != target || identity != d.localConfig["volatile.uuid"] {
			return errors.New("Migration target placement changed before staging release")
		}

		d.ovnMigrationTarget = ""
	}

	return nil
}

func (d *common) abortEmptyOVNNICMigration() error {
	if d.ovnMigrationSource == "" {
		return nil
	}

	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AbortEmptyOVNNICMigration(ctx, d.ovnMigrationSource)
	})
}

func qemuMigrationUsesLiveState(live bool, negotiated *migration.CRIUType) bool {
	return live && negotiated != nil && *negotiated == migration.CRIUType_VM_QEMU
}

func requireStagedQEMULiveState(operation string, liveState bool) error {
	if operation != "" && !liveState {
		return errors.New("Staged OVN live migration requires negotiated QEMU live state transfer before source Stop")
	}

	return nil
}

// Source hook discovery is used only after actual teardown or for its power fields.
func (d *common) ensureOVNNICSourceHookCleanupComplete() error {
	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		operation, err := tx.OVNNICMigrationSourceHook(ctx, d.id, d.localConfig["volatile.uuid"], d.localConfig)
		if err != nil {
			return err
		}

		if d.ovnMigrationSource != "" && operation != d.ovnMigrationSource {
			return errors.New("Migration source hook operation changed")
		}

		if operation != "" {
			return tx.EnsureOVNNICMigrationSourceComplete(ctx, operation, d.localConfig["volatile.uuid"])
		}

		return tx.EnsureOVNNICCleanupCompleteForInstance(ctx, d.localConfig["volatile.uuid"])
	})
}

// Preserve target restore's SQL power state while keeping this source hook stopped locally.
func (d *common) recordOVNNICSourceHookStopped() error {
	changes := map[string]string{"volatile.last_state.power": instance.PowerStateStopped, "volatile.last_state.ready": "false"}
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		operation, err := tx.OVNNICMigrationSourceHook(ctx, d.id, d.localConfig["volatile.uuid"], d.localConfig)
		if err != nil {
			return err
		}

		if d.ovnMigrationSource != "" && operation != d.ovnMigrationSource {
			return errors.New("Migration source hook operation changed")
		}

		if operation == "" && !d.volatileSetPersistDisable {
			return tx.UpdateInstanceConfig(d.id, changes)
		}

		return nil
	})
	// The local source is stopped even if its persistence authority is uncertain.
	for key, value := range changes {
		d.localConfig[key] = value
		d.expandedConfig[key] = value
	}

	return err
}

func requireStagedLXCDelayedHandover(operation string, supported bool) error {
	if operation != "" && !supported {
		return errors.New("Staged OVN live migration requires delayed liblxc final-dump handover before source Stop")
	}

	return nil
}
