package drivers

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type ovnIdentityLoadProbe struct {
	instance.Instance
	fill func(string, deviceConfig.Device) (deviceConfig.Device, error)
}

func (p ovnIdentityLoadProbe) FillNetworkDevice(name string, config deviceConfig.Device) (deviceConfig.Device, error) {
	return p.fill(name, config)
}

type ovnIdentityCleanupProbe struct {
	ovnUpdateTestDevice
}

func (p ovnIdentityCleanupProbe) Name() string { return "eth0" }
func (p ovnIdentityCleanupProbe) OVNUndoVolatile() func(func() error) error {
	return func(action func() error) error { return action() }
}

func TestOVNDeviceRollbackPersistsOriginalIdentity(t *testing.T) {
	for _, typ := range []instancetype.Type{instancetype.Container, instancetype.VM} {
		for _, running := range []bool{false, true} {
			name := typ.String() + "/stopped"
			if running {
				name = typ.String() + "/running"
			}

			t.Run(name, func(t *testing.T) {
				s, cleanup := state.NewTestState(t)
				defer cleanup()
				s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
				ctx := context.Background()
				config := deviceConfig.Device{"type": "nic", "network": "identity-network", "name": "eth0"}
				baseline := map[string]string{"volatile.uuid": uuid.NewString(), "volatile.eth0.hwaddr": "00:16:3e:11:22:33", "volatile.eth0.name": "eth0", "user.keep": "original"}
				d := &common{state: s, project: api.Project{Name: api.ProjectDefaultName}, localConfig: maps.Clone(baseline), expandedConfig: maps.Clone(baseline)}
				require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "identity-network", "", db.NetworkTypeOVN, nil)
					if err != nil {
						return err
					}

					member, err := tx.GetLocalNodeName(ctx)
					require.NoError(t, err)
					id, err := dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: api.ProjectDefaultName, Name: "identity-instance", Node: member, Architecture: 1, Type: typ})
					require.NoError(t, err)
					d.id = int(id)
					return tx.CreateInstanceConfig(ctx, d.id, baseline)
				}))

				release, err := d.reserveOVNDeviceUpdate(deviceConfig.Devices{"eth0": config})
				require.NoError(t, err)
				n, err := network.LoadByName(s, api.ProjectDefaultName, "identity-network")
				require.NoError(t, err)
				undoDevice := ovnIdentityCleanupProbe{ovnUpdateTestDevice{n: n}}

				// New-device validation clears presentation keys before the enclosing rollback restores memory.
				require.NoError(t, d.deviceVolatileReset("eth0", config, nil))
				d.localConfig, d.expandedConfig = maps.Clone(baseline), maps.Clone(baseline)
				loadErr := errors.New("stop before backend publication")
				probe := ovnIdentityLoadProbe{fill: func(name string, config deviceConfig.Device) (deviceConfig.Device, error) {
					var filled deviceConfig.Device
					var err error
					if typ == instancetype.Container {
						filled, err = (&lxc{common: *d}).FillNetworkDevice(name, config)
					} else {
						filled, err = (&qemu{common: *d}).FillNetworkDevice(name, config)
					}

					require.NoError(t, err)
					require.Equal(t, baseline["volatile.eth0.hwaddr"], filled["hwaddr"])
					return nil, loadErr
				}}
				order := []string{}
				require.True(t, d.addOVNDeviceUndo(undoDevice, func() error {
					order = append(order, "original")
					require.ErrorIs(t, d.restoreOVNDevice(probe, "eth0", config, running, nil), loadErr)
					return nil
				}))
				if running {
					// A later failure also reverses the new runtime allocation before restoring the original.
					require.True(t, d.addOVNDeviceCleanupUndo(undoDevice, func() error {
						order = append(order, "new")
						return d.deviceVolatileSetFunc("eth0")(map[string]string{"hwaddr": "00:16:3e:44:55:66", "name": "new-name"})
					}))
				}

				d.captureOVNDeviceUndo()
				require.NoError(t, d.finishOVNDeviceUpdate(false, release))
				if running {
					require.Equal(t, []string{"new", "original"}, order)
				} else {
					require.Equal(t, []string{"original"}, order)
				}

				var persisted map[string]string
				require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					var err error
					persisted, err = dbCluster.GetInstanceConfig(ctx, tx.Tx(), d.id)
					return err
				}))
				require.Equal(t, baseline, persisted, "a fresh next request must retain the original producer MAC")
				fresh := &common{state: s, project: d.project, localConfig: persisted, expandedConfig: maps.Clone(persisted)}
				var filled deviceConfig.Device
				if typ == instancetype.Container {
					filled, err = (&lxc{common: *fresh}).FillNetworkDevice("eth0", config)
				} else {
					filled, err = (&qemu{common: *fresh}).FillNetworkDevice("eth0", config)
				}

				require.NoError(t, err)
				require.Equal(t, baseline["volatile.eth0.hwaddr"], filled["hwaddr"])
			})
		}
	}
}

func TestOVNHookOrdinaryCopyPersistsStoppedState(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	identity := uuid.NewString()
	d := &common{state: &state.State{DB: &db.DB{Cluster: cluster}}, localConfig: map[string]string{"volatile.uuid": identity}, expandedConfig: map[string]string{"volatile.uuid": identity}}
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, name := range []string{"original", "ordinary-copy"} {
			member, err := tx.GetLocalNodeName(ctx)
			require.NoError(t, err)
			id, err := dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: api.ProjectDefaultName, Name: name, Node: member, Architecture: 1})
			require.NoError(t, err)
			d.id = int(id)
			err = tx.CreateInstanceConfig(ctx, int(id), map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": name + "-bridged-host", "volatile.last_state.power": instance.PowerStateRunning})
			require.NoError(t, err)
		}

		return nil
	}))
	require.NoError(t, d.acknowledgeOVNNICSourceTerminal(nil))
	require.NoError(t, d.ensureOVNNICSourceHookCleanupComplete())
	require.NoError(t, d.recordOVNNICSourceHookStopped())
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var power string
		err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.last_state.power'", d.id).Scan(&power)
		require.NoError(t, err)
		require.Equal(t, instance.PowerStateStopped, power)
		require.Error(t, tx.EnsureOVNNICOriginalInstance(ctx, d.id, identity))
		return nil
	}))
}
