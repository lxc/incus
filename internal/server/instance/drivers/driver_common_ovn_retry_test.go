package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/bgp"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/device"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type originalRetryNetwork struct {
	network.Network
	t             *testing.T
	cluster       *db.Cluster
	a             db.OVNNICCleanup
	validationErr error
	effectErr     error
	effects       int
}

func (n *originalRetryNetwork) ID() int64          { return n.a.NetworkIDs[0] }
func (n *originalRetryNetwork) UplinkName() string { return "" }
func (n *originalRetryNetwork) InstanceDevicePortValidateExternalRoutes(instance.Instance, string, []*net.IPNet) error {
	panic("unused")
}

func (n *originalRetryNetwork) InstanceDevicePortAdd(string, string, deviceConfig.Device) error {
	panic("unused")
}

func (n *originalRetryNetwork) InstanceDevicePortStart(*network.OVNInstanceNICSetupOpts, []string) (ovn.OVNSwitchPort, []net.IP, error) {
	panic("unused")
}

func (n *originalRetryNetwork) InstanceDevicePortStopCapture(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error {
	panic("must select existing original")
}

func (n *originalRetryNetwork) InstanceDevicePortRemove(string, string, deviceConfig.Device, bool) error {
	panic("unused")
}

func (n *originalRetryNetwork) InstanceDevicePortIPs(string, string) ([]net.IP, error) {
	panic("unused")
}

func (n *originalRetryNetwork) InstanceDevicePortStopSource(ctx context.Context, id, dev string) (*network.OVNInstanceNICStopOpts, error) {
	var a db.OVNNICCleanup
	err := n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		a, err = tx.OVNNICCleanupForDevice(ctx, id, dev)
		return err
	})
	if err != nil {
		return nil, err
	}

	source, _, err := network.OVNNICCleanupSource(a, n.cluster.GetNodeID(), "source-member")
	return source, err
}

func (n *originalRetryNetwork) InstanceDevicePortStopRetryValidate(ctx context.Context, id, dev, generation string) error {
	source, err := n.InstanceDevicePortStopSource(ctx, id, dev)
	if err != nil {
		return err
	}

	require.Equal(n.t, n.a.Generation, source.CleanupGeneration)
	require.Equal(n.t, n.a.Generation, generation)
	return n.validationErr
}

func (n *originalRetryNetwork) InstanceDevicePortStop(_ ovn.OVNSwitchPort, source *network.OVNInstanceNICStopOpts) error {
	n.effects++
	require.Equal(n.t, "source-host", source.HostVolatile["host_name"])
	require.Equal(n.t, "removed-original-network", source.DeviceConfig["network"])
	require.Equal(n.t, n.a.Generation, source.CleanupGeneration)
	return n.effectErr
}

func (n *originalRetryNetwork) tokens(ctx context.Context, tx *db.ClusterTx) map[int64]string {
	token, err := tx.OVNNetworkOperationToken(ctx, "default", "retry-network")
	require.NoError(n.t, err)
	require.NotEmpty(n.t, token)
	return map[int64]string{n.ID(): token}
}

func (n *originalRetryNetwork) InstanceDevicePortStopRetire(ctx context.Context, id, dev, generation string) (bool, error) {
	var cleared bool
	err := n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		a, err := tx.OVNNICCleanupByGeneration(ctx, generation)
		if err != nil {
			return err
		}

		cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, a, n.tokens(ctx, tx))
		return err
	})
	return cleared, err
}

func (n *originalRetryNetwork) InstanceDevicePortStopComplete(ctx context.Context, id, dev, generation string) error {
	return n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		a, err := tx.OVNNICCleanupByGeneration(ctx, generation)
		if err != nil {
			return err
		}

		err = tx.EnsureOVNNICCleanupRetired(ctx, a)
		if err != nil {
			return err
		}

		return tx.CompleteOVNNICCleanup(ctx, a, n.tokens(ctx, tx))
	})
}

// Only private CowSQL and inert nested NIC effects are initialized; no runtime driver is started.
func originalRetryFixture(t *testing.T, typ instancetype.Type) (*common, *originalRetryNetwork, db.OVNNICCleanupRetry) {
	t.Helper()
	ctx := context.Background()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	d := &common{project: api.Project{Name: "default"}, name: "retry-" + uuid.NewString(), dbType: typ, logger: logger.AddContext(logger.Ctx{}), state: &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: ctx, ServerName: "source-member", BGP: bgp.NewServer(), OVN: func() (*ovn.NB, *ovn.SB, error) { return nil, nil, nil }}, localConfig: map[string]string{}, expandedConfig: map[string]string{}}
	identity, operation, token := uuid.NewString(), uuid.NewString(), uuid.NewString()
	var target int64
	var a db.OVNNICCleanup
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		networkID, err := tx.CreateNetwork(ctx, "default", "retry-network", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		if err = tx.NetworkCreated("default", "retry-network"); err != nil {
			return err
		}

		if err = tx.NetworkNodeCreated(networkID); err != nil {
			return err
		}

		res, err := tx.Tx().ExecContext(ctx, `INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,?,'',(SELECT id FROM projects WHERE name='default'))`, tx.GetNodeID(), d.name, typ)
		if err != nil {
			return err
		}

		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		d.id = int(id)
		d.localConfig = map[string]string{"volatile.uuid": identity, "volatile.eth0.host_name": "source-host", "volatile.last_state.power": "STOPPED", "volatile.last_state.ready": "false"}
		for key, value := range d.localConfig {
			if _, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES(?,?,?)", id, key, value); err != nil {
				return err
			}
		}

		port := ovn.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), SwitchUUID: uuid.NewString(), PortUUID: uuid.NewString(), PortVersion: uuid.NewString(), SwitchName: "source-switch", PortName: "source-port", Source: "source-member"}
		raw, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "NetworkID": networkID, "DeviceConfig": deviceConfig.Device{"type": "nic", "network": "removed-original-network", "nested": "parent"}, "HostVolatile": map[string]string{"host_name": "source-host"}, "Port": port})
		if err != nil {
			return err
		}

		a = db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: cluster.GetNodeID(), InstanceUUID: identity, DeviceName: "eth0", Version: 1, NetworkIDs: []int64{networkID}, Payload: string(raw)}
		if err = tx.AcquireOVNNetworkOperation(ctx, "default", "retry-network", token, "nic"); err != nil {
			return err
		}

		if err = tx.CaptureOVNNICCleanup(ctx, a, map[int64]string{networkID: token}); err != nil {
			return err
		}

		target, err = tx.CreateNode("retry-target", "192.0.2.22:8443")
		if err != nil {
			return err
		}

		return tx.AuthorizeOVNNICMigration(ctx, operation, d.id, identity, target)
	}))
	sourceNode := cluster.GetNodeID()
	cluster.NodeID(target)
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		claim, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": a.NetworkIDs[0], "SourceNodeID": target, "InstanceID": d.id, "InstanceUUID": identity, "DeviceName": "eth0"})
		if err != nil {
			return err
		}

		err = tx.SetOVNNICMigrationVolatile(ctx, operation, d.id, identity, map[string]string{"volatile.eth0.host_name": "target-host", "volatile.eth0.last_state.ovn.host": string(claim)})
		if err != nil {
			return err
		}

		m, err := tx.OVNNICMigrationDevice(ctx, operation, "eth0")
		if err != nil {
			return err
		}

		if err = tx.SetOVNNICMigrationShared(ctx, m, `{"root":"inert immutable backend"}`); err != nil {
			return err
		}

		if err = tx.SetOVNNICMigrationOVS(ctx, operation, "eth0", `{"root":"inert-ovs"}`, true); err != nil {
			return err
		}

		return tx.OVNNICMigrationReady(ctx, operation, "eth0")
	}))
	cluster.NodeID(sourceNode)
	d.expandedConfig = maps.Clone(d.localConfig)
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.OVNNICMigrationHandover(ctx, operation) }))
	require.NoError(t, d.recordOVNNICSourceTerminal())
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.PlaceOVNNICMigration(ctx, operation, d.id, identity, target)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", target, d.id)
		if err != nil {
			return err
		}

		if err = tx.UpdateInstanceConfig(d.id, map[string]string{"volatile.last_state.power": "RUNNING", "volatile.last_state.ready": "true"}); err != nil {
			return err
		}

		return tx.ReleaseOVNNetworkOperation(ctx, "default", "retry-network", token)
	}))
	d.localConfig["volatile.eth0.host_name"] = "target-host"
	d.localConfig["volatile.last_state.power"] = "RUNNING"
	d.localConfig["volatile.last_state.ready"] = "true"
	d.expandedConfig = maps.Clone(d.localConfig)
	d.expandedDevices = deviceConfig.Devices{"eth0": {"type": "disk", "path": "/changed"}}
	plan, err := d.originalOVNCleanupRetry()
	require.NoError(t, err)
	require.NotNil(t, plan)
	return d, &originalRetryNetwork{t: t, cluster: cluster, a: a}, *plan
}

func TestDriverOVNTransferredMaintenanceOriginalReplay(t *testing.T) {
	for _, typ := range []instancetype.Type{instancetype.Container, instancetype.VM} {
		for _, mode := range []string{"success", "backend-error", "preflight-root", "outstanding-operation", "receipt-replaced", "new-target-generation", "target-stopped"} {
			t.Run(typ.String()+"/"+mode, func(t *testing.T) {
				d, n, plan := originalRetryFixture(t, typ)
				ctx := context.Background()
				sentinel := errors.New("inert original backend failure")
				switch mode {
				case "backend-error":
					n.effectErr = sentinel
				case "preflight-root":
					n.validationErr = errors.New("original rooted lineage replaced")
				}

				if mode == "outstanding-operation" || mode == "receipt-replaced" || mode == "new-target-generation" || mode == "target-stopped" {
					require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						switch mode {
						case "outstanding-operation":
							return tx.AcquireOVNNetworkOperation(ctx, "default", "retry-network", uuid.NewString(), "prepare")
						case "receipt-replaced":
							_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_terminal='{}' WHERE operation=?", plan.Operation)
							return err
						case "new-target-generation":
							return tx.UpdateInstanceConfig(d.id, map[string]string{"volatile.eth0.host_name": "new-target-host", "volatile.eth0.last_state.ovn.host": `{"SourceNodeID":2,"generation":"new"}`})
						case "target-stopped":
							return tx.UpdateInstanceConfig(d.id, map[string]string{"volatile.eth0.host_name": "", "volatile.last_state.power": "STOPPED", "volatile.last_state.ready": "false"})
						}

						return nil
					}))
				}

				localBefore := maps.Clone(d.localConfig)
				expandedBefore := maps.Clone(d.expandedConfig)
				var inst instance.Instance
				var stop func(device.Device) error
				if typ == instancetype.Container {
					driver := &lxc{common: *d}
					d = &driver.common
					inst = driver
					stop = func(dev device.Device) error { return driver.deviceStop(dev, false, "") }
				} else {
					driver := &qemu{common: *d}
					d = &driver.common
					inst = driver
					stop = func(dev device.Device) error { return driver.deviceStop(dev, false, "") }
				}

				err := d.retryTransferredOVNStop(inst, plan, func(source *network.OVNInstanceNICStopOpts) (device.Device, error) {
					return device.NewOVNStopCleanup(inst, d.state, n, source)
				}, stop)
				switch mode {
				case "backend-error":
					require.ErrorIs(t, err, sentinel)
				case "preflight-root", "outstanding-operation", "receipt-replaced":
					require.Error(t, err)
					require.Zero(t, n.effects)
				default:
					require.NoError(t, err)
					require.Equal(t, 1, n.effects)
				}

				require.Equal(t, localBefore, d.localConfig)
				require.Equal(t, expandedBefore, d.expandedConfig)
				require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					a, err := tx.OVNNICCleanupByGeneration(ctx, n.a.Generation)
					if err != nil {
						return err
					}

					success := mode == "success" || mode == "new-target-generation" || mode == "target-stopped"
					require.Equal(t, success, a.Completed)
					if success {
						require.NoError(t, tx.EnsureOVNNICCleanupRetired(ctx, a))
						require.NoError(t, tx.EnsureOVNNICMigrationSourceComplete(ctx, plan.Operation, a.InstanceUUID))
					} else {
						require.Error(t, tx.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, a.InstanceUUID))
					}

					var host, power, ready string
					err = tx.Tx().QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'),''),(SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.last_state.power'),(SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.last_state.ready')`, d.id, d.id, d.id).Scan(&host, &power, &ready)
					if err != nil {
						return err
					}

					wantHost, wantPower, wantReady := "target-host", "RUNNING", "true"
					if mode == "new-target-generation" {
						wantHost = "new-target-host"
					}

					if mode == "target-stopped" {
						wantHost = ""
						wantPower = "STOPPED"
						wantReady = "false"
					}

					require.Equal(t, wantHost, host)
					require.Equal(t, wantPower, power)
					require.Equal(t, wantReady, ready)
					return nil
				}))
			})
		}
	}
}

func TestDriverOVNSourceTerminalOutcome(t *testing.T) {
	for _, mode := range []string{"terminal-failure", "positive-terminal-pending-NIC", "receipt-persistence-failure", "target-local-refusal"} {
		t.Run(mode, func(t *testing.T) {
			d, n, plan := originalRetryFixture(t, instancetype.Container)
			ctx := context.Background()
			require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET source_terminal='' WHERE operation=?", plan.Operation)
				if err != nil {
					return err
				}

				if mode == "receipt-persistence-failure" {
					_, err = tx.Tx().ExecContext(ctx, `CREATE TRIGGER refuse_driver_terminal BEFORE UPDATE OF source_terminal ON networks_ovn_nic_migrations BEGIN SELECT RAISE(ABORT, 'driver terminal persistence failure'); END`)
				}

				return err
			}))
			if mode != "target-local-refusal" {
				d.localConfig["volatile.eth0.host_name"] = "source-host"
			}

			failure := errors.New("actual source non-NIC runtime failure")
			var terminalErr error
			if mode == "terminal-failure" {
				terminalErr = failure
			}

			err := d.acknowledgeOVNNICSourceTerminal(terminalErr)
			switch mode {
			case "positive-terminal-pending-NIC":
				require.NoError(t, err)
			default:
				require.Error(t, err)
			}

			if mode == "terminal-failure" {
				require.ErrorIs(t, err, failure)
			}

			require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				m, err := tx.OVNNICMigrationDevice(ctx, plan.Operation, "eth0")
				if err != nil {
					return err
				}

				switch mode {
				case "positive-terminal-pending-NIC":
					require.NotEmpty(t, m.SourceTerminal)
				default:
					require.Empty(t, m.SourceTerminal)
				}

				require.False(t, m.Source.Completed)
				require.Error(t, tx.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, n.a.InstanceUUID))
				return nil
			}))
		})
	}
}

func TestDriverOVNUserUpdateRejectsBorrowedClaimBeforeEffects(t *testing.T) {
	for _, typ := range []instancetype.Type{instancetype.Container, instancetype.VM} {
		t.Run(typ.String(), func(t *testing.T) {
			d, n, _ := originalRetryFixture(t, typ)
			before := maps.Clone(d.localConfig)
			var storedClaim string
			require.NoError(t, n.cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", d.id).Scan(&storedClaim)
			}))
			args := db.InstanceArgs{Config: maps.Clone(before)}
			args.Config["volatile.eth0.last_state.ovn.host"] = "foreign-host-claim"
			var err error
			if typ == instancetype.Container {
				err = (&lxc{common: *d}).Update(args, true)
			} else {
				err = (&qemu{common: *d}).Update(args, true)
			}

			require.True(t, api.StatusErrorCheck(err, 409), "%v", err)
			require.Equal(t, before, d.localConfig)
			require.Zero(t, n.effects)
			require.NoError(t, n.cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var current string
				err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", d.id).Scan(&current)
				require.Equal(t, storedClaim, current)
				return err
			}))
		})
	}
}

func TestDriverOVNUpdateCommitUsesInternallyUpdatedClaim(t *testing.T) {
	d, n, _ := originalRetryFixture(t, instancetype.Container)
	ctx := context.Background()
	var original string
	require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", d.id).Scan(&original)
	}))
	d.localConfig["volatile.eth0.last_state.ovn.host"] = original
	request := maps.Clone(d.localConfig)
	require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.ValidateInstanceOVNConfigUpdate(ctx, d.id, request)
	}))
	require.NoError(t, d.VolatileSet(map[string]string{"volatile.eth0.last_state.ovn.host": "internally-republished-own-claim"}))
	require.NoError(t, n.cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		require.Error(t, tx.ValidateInstanceOVNConfigUpdate(ctx, d.id, request), "the original client snapshot is stale after legitimate device effects")
		return tx.ValidateInstanceOVNConfigUpdate(ctx, d.id, d.localConfig)
	}))
}

func TestDriverOVNMaintenanceNoDebtAndOrdinaryTerminal(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	identity := uuid.NewString()
	id := 0
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		res, err := tx.Tx().ExecContext(ctx, `INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'ordinary-no-NIC',1,0,'',(SELECT id FROM projects WHERE name='default'))`, tx.GetNodeID())
		if err != nil {
			return err
		}

		n, err := res.LastInsertId()
		id = int(n)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", id, identity)
		return err
	}))
	d := common{id: id, name: "ordinary-no-NIC", project: api.Project{Name: "default"}, state: &state.State{DB: &db.DB{Cluster: cluster}}, localConfig: map[string]string{"volatile.uuid": identity}}
	require.NoError(t, (&lxc{common: d}).RetryOVNNICCleanup())
	require.NoError(t, (&qemu{common: d}).RetryOVNNICCleanup())
	require.NoError(t, d.acknowledgeOVNNICSourceTerminal(nil))
}
