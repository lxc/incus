package device

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/state"
)

type nicPrecopyInstance struct {
	nicStartAdmissionInstance
	id int
}

func (i *nicPrecopyInstance) ID() int { return i.id }

type nicPrecopyNetwork struct {
	ovnNet
	adds int
}

func (n *nicPrecopyNetwork) InstanceDevicePortAdd(string, string, deviceConfig.Device) error {
	n.adds++
	return nil
}

func TestNICOVNPrecopyDefersSharedPortEffects(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	identity := uuid.NewString()
	var sourceID, targetID int64
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, item := range []struct {
			name, uuid string
			id         *int64
		}{
			{"source", identity, &sourceID},
			{"foreign", uuid.NewString(), new(int64)},
			{"precopy", identity, &targetID},
		} {
			res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,0,'',1)", tx.GetNodeID(), item.name)
			if err != nil {
				return err
			}
			*item.id, err = res.LastInsertId()
			if err != nil {
				return err
			}

			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", *item.id, item.uuid)
			if err != nil {
				return err
			}
		}

		_, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.eth0.last_state.ovn.host','original-source-claim')", sourceID)
		return err
	}))

	net := &nicPrecopyNetwork{}
	d := &nicOVN{network: net, deviceCommon: deviceCommon{
		inst: &nicPrecopyInstance{id: int(targetID), nicStartAdmissionInstance: nicStartAdmissionInstance{config: map[string]string{"volatile.uuid": identity}}},
		name: "eth0", config: deviceConfig.Device{"network": "dynamic-ovn"},
		state:       &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: cluster}},
		volatileGet: func() map[string]string { return nil },
	}}
	require.NoError(t, d.Add())
	require.Zero(t, net.adds, "pre-copy cannot publish a disabled port over the running source")
	require.NoError(t, d.Remove(true), "pre-copy cancellation cannot remove its source's port")
	require.NoError(t, d.Update(deviceConfig.Devices{"eth0": d.config.Clone()}, false), "a stopped newer record cannot retire or republish the original port")
	require.Zero(t, net.adds)
	starts := 0
	start := func() (*deviceConfig.RunConfig, error) { starts++; return nil, nil }
	_, err := d.startWithCleanupAdmission(start)
	require.ErrorContains(t, err, "Original instance still owns")
	require.Zero(t, starts)

	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var raw string
		err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", sourceID).Scan(&raw)
		require.Equal(t, "original-source-claim", raw)
		if err != nil {
			return err
		}

		original, err := tx.OVNNICHasOriginalInstance(ctx, int(sourceID), identity)
		require.False(t, original, "an original source remains eligible for rollback restart")
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "DELETE FROM instances WHERE id=?", sourceID)
		return err
	}))

	require.NoError(t, d.Add())
	require.Equal(t, 1, net.adds, "foreign UUIDs cannot defer this sole remaining owner")
	_, err = d.startWithCleanupAdmission(start)
	require.NoError(t, err)
	require.Equal(t, 1, starts)
}
