package device

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/state"
)

type nicStagedTargetInstance struct {
	nicStopCaptureInstance
	operation string
}

func (i *nicStagedTargetInstance) OVNNICMigrationOperation() string { return i.operation }

func TestNICOVNMigrationTwoNICActualAdmissionAndRetainedClaim(t *testing.T) {
	cluster, closeCluster := db.NewTestCluster(t)
	t.Cleanup(closeCluster)
	op := uuid.NewString()
	inst := &nicStagedTargetInstance{operation: op}
	identity := inst.LocalConfig()["volatile.uuid"]
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		source, err := tx.CreateNode("original-source", "192.0.2.50:8443")
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances(id,node_id,name,architecture,type,description,project_id) VALUES (19,?,'moving',1,0,'',1)", source)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (19,'volatile.uuid',?)", identity)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) VALUES (?,1,19,?,?,?,'authorized')", op, identity, source, cluster.GetNodeID())
		if err != nil {
			return err
		}

		for _, name := range []string{"eth0", "eth1"} {
			payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": 19, "HostVolatile": map[string]string{}})
			if err != nil {
				return err
			}

			gen := uuid.NewString()
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload) VALUES (?,?,?,?,1,'[41]',?)", gen, source, identity, name, string(payload))
			if err != nil {
				return err
			}

			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_devices(operation,device_name,generation) VALUES (?,?,?)", op, name, gen)
			if err != nil {
				return err
			}
		}

		return nil
	}))
	entered := 0
	d := &nicOVN{deviceCommon: deviceCommon{inst: inst, name: "eth0", config: deviceConfig.Device{"type": "nic", "network": "unchanged"}, state: &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background()}}}
	_, err := d.startWithCleanupAdmission(func() (*deviceConfig.RunConfig, error) { entered++; return nil, nil })
	require.NoError(t, err)
	require.Equal(t, 1, entered)
	raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "SourceNodeID": cluster.GetNodeID(), "InstanceID": 19, "InstanceUUID": identity, "DeviceName": "eth0", "Alias": "finite-target-generation"})
	require.NoError(t, err)
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.SetOVNNICMigrationVolatile(ctx, op, 19, identity, map[string]string{"volatile.eth0.host_name": "target-original", "volatile.eth0.last_state.ovn.host": string(raw)})
	}))
	d.name = "eth1"
	_, err = d.startWithCleanupAdmission(func() (*deviceConfig.RunConfig, error) { entered++; return nil, nil })
	require.NoError(t, err)
	require.Equal(t, 2, entered)
	d.name = "eth0"
	_, err = d.Start()
	require.ErrorContains(t, err, "retains an allocation")
	require.Equal(t, 2, entered)
	inst.operation = uuid.NewString()
	_, err = d.Start()
	require.Error(t, err)
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		m, err := tx.OVNNICMigrationDevice(ctx, op, "eth0")
		if err != nil {
			return err
		}

		require.Equal(t, "target-original", m.TargetVolatile["host_name"])
		return nil
	}))
}
