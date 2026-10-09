package drivers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/device"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/network"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type ovnUpdateTestDevice struct {
	device.Device
	n network.Network
}

func (d ovnUpdateTestDevice) OVNNetwork() network.Network {
	return d.n
}

func TestOVNInstanceUpdateReservationSpansDeviceAndDatabaseCommit(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "commit"
		if rollback {
			name = "rollback"
		}

		t.Run(name, func(t *testing.T) {
			s, cleanup := state.NewTestState(t)
			defer cleanup()
			s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
			old := deviceConfig.Devices{"eth0": {"type": "nic", "network": "ovn-a", "security.acls": "old"}}
			next := db.ExpandInstanceDevices(deviceConfig.Devices{"eth0": {"type": "nic", "network": "ovn-a", "security.acls": "new"}}, []api.Profile{{ProfilePut: api.ProfilePut{Devices: map[string]map[string]string{"eth1": {"type": "nic", "network": "ovn-b"}}}}})
			d := &common{state: s, project: api.Project{Name: api.ProjectDefaultName}, expandedDevices: old}
			ctx := context.Background()
			var instanceID int64
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, name := range []string{"ovn-a", "ovn-b", "unrelated"} {
					_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, name, "", db.NetworkTypeOVN, nil)
					require.NoError(t, err)
				}

				member, err := tx.GetLocalNodeName(ctx)
				require.NoError(t, err)
				instanceID, err = dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: api.ProjectDefaultName, Name: "instance", Node: member, Architecture: 1})
				require.NoError(t, err)
				devices, err := dbCluster.APIToDevices(old.CloneNative())
				require.NoError(t, err)
				return dbCluster.CreateInstanceDevices(ctx, tx.Tx(), instanceID, devices)
			})
			require.NoError(t, err)

			release, err := d.reserveOVNDeviceUpdate(next)
			require.NoError(t, err)
			require.Len(t, d.ovnDeviceUpdateOperations, 2)
			n, err := network.LoadByName(s, api.ProjectDefaultName, "ovn-a")
			require.NoError(t, err)
			require.NoError(t, d.authorizeOVNDeviceUpdate(ovnUpdateTestDevice{n: n}))
			deviceRelease, err := network.AcquireOVNNICOperation(n, true)
			require.NoError(t, err)
			require.NoError(t, deviceRelease())

			// Device work has returned, but ACL deletion must still fail while the database shows old references.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				devices, err := dbCluster.GetInstanceDevices(ctx, tx.Tx(), int(instanceID))
				require.NoError(t, err)
				require.Equal(t, "old", devices["eth0"].Config["security.acls"])
				require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "acl-delete", "acl-config", false), http.StatusConflict))
				require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "ovn-b", "delete", "delete"), http.StatusConflict))
				require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "unrelated", "other", "update"))
				return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "unrelated", "other")
			})
			require.NoError(t, err)

			rollbackErr := errors.New("modeled commit rollback")
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				devices, err := dbCluster.APIToDevices(next.CloneNative())
				require.NoError(t, err)
				err = dbCluster.UpdateInstanceDevices(ctx, tx.Tx(), instanceID, devices)
				require.NoError(t, err)
				if rollback {
					return rollbackErr
				}

				return nil
			})
			if rollback {
				require.ErrorIs(t, err, rollbackErr)
			} else {
				require.NoError(t, err)
			}

			// Rollback handlers and post-commit work still own the same reservation until the outer defer runs.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "acl-delete", "acl-config", false), http.StatusConflict))
				devices, err := dbCluster.GetInstanceDevices(ctx, tx.Tx(), int(instanceID))
				require.NoError(t, err)
				if rollback {
					require.Equal(t, "old", devices["eth0"].Config["security.acls"])
				} else {
					require.Equal(t, "new", devices["eth0"].Config["security.acls"])
				}

				return nil
			})
			require.NoError(t, err)
			require.NoError(t, release())
			require.Nil(t, d.ovnDeviceUpdateOperations)
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.AcquireOVNPeerOperation(ctx, "acl-delete", "acl-config", false)
			})
			require.NoError(t, err)
		})
	}
}

func TestOVNInstanceUpdateRollbackAcknowledgment(t *testing.T) {
	for _, outcome := range []string{"committed", "reversed", "uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			s, cleanup := state.NewTestState(t)
			defer cleanup()
			s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
			ctx := context.Background()
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "undo-network", "", db.NetworkTypeOVN, nil)
				return err
			})
			require.NoError(t, err)
			d := &common{state: s, project: api.Project{Name: api.ProjectDefaultName}}
			release, err := d.reserveOVNDeviceUpdate(deviceConfig.Devices{"eth0": {"type": "nic", "network": "undo-network"}})
			require.NoError(t, err)
			n, err := network.LoadByName(s, api.ProjectDefaultName, "undo-network")
			require.NoError(t, err)
			dev := ovnUpdateTestDevice{n: n}
			order := []int{}
			for _, step := range []int{1, 2} {
				require.True(t, d.addOVNDeviceUndo(dev, func() error {
					order = append(order, step)
					err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "acl-delete", "acl-config", false), http.StatusConflict))
						return nil
					})
					require.NoError(t, err)
					if outcome == "uncertain" && step == 2 {
						return context.DeadlineExceeded
					}

					return nil
				}))
			}

			err = d.finishOVNDeviceUpdate(outcome == "committed", release)
			if outcome == "uncertain" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorContains(t, err, "reservations are retained")
			} else {
				require.NoError(t, err)
			}

			if outcome == "committed" {
				require.Empty(t, order)
			} else {
				require.Equal(t, []int{2, 1}, order)
			}

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "undo-network")
				require.NoError(t, err)
				require.Equal(t, outcome == "uncertain", token != "")
				return nil
			})
			require.NoError(t, err)
			unlock, err := network.LockOVNLifecycle(api.ProjectDefaultName, "undo-network")
			require.NoError(t, err)
			unlock()
		})
	}
}
