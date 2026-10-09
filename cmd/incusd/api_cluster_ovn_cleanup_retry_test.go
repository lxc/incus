package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type maintenanceOriginalInstance struct {
	instance.Instance
	id       int
	name     string
	identity string
	retry    func() error
}

func (i *maintenanceOriginalInstance) ID() int              { return i.id }
func (i *maintenanceOriginalInstance) Name() string         { return i.name }
func (i *maintenanceOriginalInstance) Project() api.Project { return api.Project{Name: "default"} }
func (i *maintenanceOriginalInstance) LocalConfig() map[string]string {
	return map[string]string{"volatile.uuid": i.identity, "volatile.eth0.host_name": "current-target-host"}
}

func (i *maintenanceOriginalInstance) RetryOVNNICCleanup() error { return i.retry() }
func (i *maintenanceOriginalInstance) Stop(bool) error {
	panic("maintenance may not call ordinary target Stop")
}

// This fixture runs only a private CowSQL cluster and production stage SQL methods.
func maintenanceOriginalFixture(t *testing.T) (*state.State, *maintenanceOriginalInstance, string) {
	t.Helper()
	ctx := context.Background()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	s := &state.State{DB: &db.DB{Cluster: cluster}, ServerName: "source-member", ShutdownCtx: ctx}
	inst := &maintenanceOriginalInstance{name: "post-placement", identity: uuid.NewString()}
	operation, token := uuid.NewString(), uuid.NewString()
	var target, networkID int64
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "UPDATE nodes SET name=? WHERE id=?", s.ServerName, tx.GetNodeID())
		if err != nil {
			return err
		}

		networkID, err = tx.CreateNetwork(ctx, "default", "caller-retry", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		if err = tx.NetworkCreated("default", "caller-retry"); err != nil {
			return err
		}

		if err = tx.NetworkNodeCreated(networkID); err != nil {
			return err
		}

		res, err := tx.Tx().ExecContext(ctx, `INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,0,'',(SELECT id FROM projects WHERE name='default'))`, tx.GetNodeID(), inst.name)
		if err != nil {
			return err
		}

		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		inst.id = int(id)
		for key, value := range map[string]string{"volatile.uuid": inst.identity, "volatile.eth0.host_name": "original-source-host"} {
			if _, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES(?,?,?)", id, key, value); err != nil {
				return err
			}
		}

		raw, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "NetworkID": networkID, "HostVolatile": map[string]string{"host_name": "original-source-host"}})
		if err != nil {
			return err
		}

		a := db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: cluster.GetNodeID(), InstanceUUID: inst.identity, DeviceName: "eth0", Version: 1, NetworkIDs: []int64{networkID}, Payload: string(raw)}
		if err = tx.AcquireOVNNetworkOperation(ctx, "default", "caller-retry", token, "nic"); err != nil {
			return err
		}

		if err = tx.CaptureOVNNICCleanup(ctx, a, map[int64]string{networkID: token}); err != nil {
			return err
		}

		target, err = tx.CreateNode("caller-target", "192.0.2.44:8443")
		if err != nil {
			return err
		}

		return tx.AuthorizeOVNNICMigration(ctx, operation, inst.id, inst.identity, target)
	}))
	source := cluster.GetNodeID()
	cluster.NodeID(target)
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		claim, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": networkID, "SourceNodeID": target, "InstanceID": inst.id, "InstanceUUID": inst.identity, "DeviceName": "eth0"})
		if err != nil {
			return err
		}

		if err = tx.SetOVNNICMigrationVolatile(ctx, operation, inst.id, inst.identity, map[string]string{"volatile.eth0.host_name": "current-target-host", "volatile.eth0.last_state.ovn.host": string(claim)}); err != nil {
			return err
		}

		m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
		if err != nil {
			return err
		}

		if err = tx.SetOVNNICMigrationShared(ctx, m, `{"root":"inert fixture"}`); err != nil {
			return err
		}

		if err = tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"root":"inert fixture"}`, true); err != nil {
			return err
		}

		return tx.OVNNICMigrationReady(ctx, operation, "eth0")
	}))
	cluster.NodeID(source)
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.OVNNICMigrationHandover(ctx, operation)
		if err != nil {
			return err
		}

		err = tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, inst.id, inst.identity, map[string]string{"volatile.eth0.host_name": "original-source-host"})
		if err != nil {
			return err
		}

		err = tx.PlaceOVNNICMigration(ctx, operation, inst.id, inst.identity, target)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, inst.id)
		if err != nil {
			return err
		}

		return tx.ReleaseOVNNetworkOperation(ctx, "default", "caller-retry", token)
	}))
	return s, inst, operation
}

func TestOVNNICMaintenanceSelectsOriginalAfterPlacement(t *testing.T) {
	for _, mode := range []string{"selection", "visible-error", "loader-replaced", "receipt-missing", "cross-member"} {
		t.Run(mode, func(t *testing.T) {
			s, inst, operation := maintenanceOriginalFixture(t)
			ctx := context.Background()
			require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				current, err := dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Node: &s.ServerName})
				require.Empty(t, current, "ordinary evacuation current-node selection misses placed original")
				return err
			}))
			if mode == "receipt-missing" {
				require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_terminal='' WHERE operation=?", operation)
					return err
				}))
			}

			calls, loads, before := 0, 0, 0
			sentinel := errors.New("original source recovery failure")
			inst.retry = func() error {
				calls++
				if mode == "visible-error" {
					return sentinel
				}

				return nil
			}

			member := s.ServerName
			if mode == "cross-member" {
				member = "other-member"
			}

			err := retryOriginalOVNMaintenance(ctx, s, member, func(project, name string) (instance.Instance, error) {
				loads++
				require.Equal(t, "default", project)
				require.Equal(t, inst.name, name)
				if mode == "loader-replaced" {
					inst.id++
				}

				return inst, nil
			}, func() { before++ })
			switch mode {
			case "selection":
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, 1, before)
			case "visible-error":
				require.ErrorIs(t, err, sentinel)
				require.Equal(t, 1, calls)
				require.Equal(t, 1, before)
			default:
				require.Error(t, err)
				require.Zero(t, calls)
				require.Zero(t, before)
			}

			if mode == "receipt-missing" || mode == "cross-member" {
				require.Zero(t, loads)
			}
			// A successful selection callback alone never retires or acknowledges debt.
			require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				require.Error(t, tx.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, inst.identity))
				return nil
			}))
		})
	}
}

func TestOVNNICMaintenanceNoDebtControl(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	s := &state.State{DB: &db.DB{Cluster: cluster}, ServerName: "source-member"}
	require.NoError(t, retryOriginalOVNMaintenance(context.Background(), s, s.ServerName, func(string, string) (instance.Instance, error) {
		t.Fatal("no debt may load no instance")
		return nil, nil
	}, func() { t.Fatal("no debt may start no effects") }))
}
