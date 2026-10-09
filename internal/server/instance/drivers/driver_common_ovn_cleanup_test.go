package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/device"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v7/internal/server/instance/operationlock"
	"github.com/lxc/incus/v7/internal/server/network"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type stoppedCleanupDevice struct {
	device.Device
	name    string
	hookErr error
}

func (d *stoppedCleanupDevice) Name() string { return d.name }
func (d *stoppedCleanupDevice) Config() deviceConfig.Device {
	return deviceConfig.Device{"type": "nic"}
}

func (d *stoppedCleanupDevice) Stop() (*deviceConfig.RunConfig, error) {
	return &deviceConfig.RunConfig{PostHooks: []func() error{func() error { return d.hookErr }}}, nil
}

func TestStoppedDeviceCleanupContinuesAndReports(t *testing.T) {
	for _, name := range []string{"success", "first-error", "both-errors", "load-error", "usable-validation-warning", "unsupported", "nil-device"} {
		t.Run(name, func(t *testing.T) {
			d := &common{logger: logger.AddContext(logger.Ctx{})}
			firstErr, secondErr, validationErr := errors.New("first cleanup"), errors.New("second cleanup"), errors.New("validation")
			entries := []deviceConfig.DeviceNamed{{Name: "first"}, {Name: "second"}}
			var loads, stops []string
			err := d.cleanupStoppedDevices(entries, func(deviceConfig.DeviceNamed) bool { return true },
				func(entry deviceConfig.DeviceNamed) (device.Device, error) {
					loads = append(loads, entry.Name)
					dev := &stoppedCleanupDevice{name: entry.Name}
					if entry.Name == "first" {
						switch name {
						case "load-error":
							return nil, validationErr
						case "usable-validation-warning":
							return dev, validationErr
						case "unsupported":
							return nil, device.ErrUnsupportedDevType
						case "nil-device":
							return nil, nil
						}
					}

					return dev, nil
				}, func(dev device.Device) error {
					stops = append(stops, dev.Name())
					if dev.Name() == "first" && (name == "first-error" || name == "both-errors") {
						return firstErr
					}

					if dev.Name() == "second" && name == "both-errors" {
						return secondErr
					}

					return nil
				})
			require.Equal(t, []string{"first", "second"}, loads)
			if name == "load-error" || name == "unsupported" || name == "nil-device" {
				require.Equal(t, []string{"second"}, stops)
			} else {
				require.Equal(t, []string{"first", "second"}, stops)
			}

			switch name {
			case "first-error":
				require.ErrorIs(t, err, firstErr)
			case "both-errors":
				require.ErrorIs(t, err, firstErr)
				require.ErrorIs(t, err, secondErr)
			case "load-error":
				require.ErrorIs(t, err, validationErr)
			case "nil-device":
				require.ErrorContains(t, err, "no usable device")
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestStoppedDeviceCleanupLXCPhaseSelection(t *testing.T) {
	for _, phase := range []string{"netns", ""} {
		name := "network-namespace"
		if phase == "" {
			name = "post-stop"
		}

		t.Run(name, func(t *testing.T) {
			d := &common{logger: logger.AddContext(logger.Ctx{})}
			entries := []deviceConfig.DeviceNamed{
				{Name: "nic-1", Config: deviceConfig.Device{"type": "nic"}},
				{Name: "disk", Config: deviceConfig.Device{"type": "disk"}},
				{Name: "nic-2", Config: deviceConfig.Device{"type": "nic"}},
			}

			var stopped []string
			err := d.cleanupStoppedDevices(entries, func(entry deviceConfig.DeviceNamed) bool { return lxcStopCleanupSelect(entry, phase) },
				func(entry deviceConfig.DeviceNamed) (device.Device, error) {
					return &stoppedCleanupDevice{name: entry.Name}, nil
				},
				func(dev device.Device) error { stopped = append(stopped, dev.Name()); return nil })
			require.NoError(t, err)
			switch phase {
			case "":
				require.Equal(t, []string{"disk"}, stopped)
			default:
				require.Equal(t, []string{"nic-1", "nic-2"}, stopped)
			}
		})
	}
}

// Literal drivers, fake devices and RunConfig containing only counted/error
// hooks. instanceRunning=false and empty interface/mount/cgroup requests avoid
// QMP, liblxc container construction, storage/host effects and operation setup.
func TestDriverStopCleanupHookErrorBatch(t *testing.T) {
	for _, driver := range []string{"qemu", "lxc"} {
		t.Run(driver, func(t *testing.T) {
			for _, failures := range []string{"none", "first", "second", "both"} {
				t.Run(failures, func(t *testing.T) {
					firstErr, secondErr := errors.New("first hook"), errors.New("second hook")
					base := common{logger: logger.AddContext(logger.Ctx{})}
					q, l := &qemu{common: base}, &lxc{common: base}
					var stopCalls []string
					err := base.cleanupStoppedDevices([]deviceConfig.DeviceNamed{{Name: "first"}, {Name: "second"}},
						func(deviceConfig.DeviceNamed) bool { return true },
						func(entry deviceConfig.DeviceNamed) (device.Device, error) {
							d := &stoppedCleanupDevice{name: entry.Name}
							if entry.Name == "first" && (failures == "first" || failures == "both") {
								d.hookErr = firstErr
							}

							if entry.Name == "second" && (failures == "second" || failures == "both") {
								d.hookErr = secondErr
							}

							return d, nil
						}, func(dev device.Device) error {
							stopCalls = append(stopCalls, dev.Name())
							if driver == "qemu" {
								return q.deviceStop(dev, false, "")
							}

							return l.deviceStop(dev, false, "")
						})
					require.Equal(t, []string{"first", "second"}, stopCalls)
					if failures == "first" || failures == "both" {
						require.ErrorIs(t, err, firstErr)
					}

					if failures == "second" || failures == "both" {
						require.ErrorIs(t, err, secondErr)
					}

					if failures == "none" {
						require.NoError(t, err)
					}
				})
			}
		})
	}
}

func TestDriverStopCleanupDebtGuard(t *testing.T) {
	for _, name := range []string{"no-record", "pending", "other-instance", "completed", "query-error-retry"} {
		t.Run(name, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			id := uuid.NewString()
			d := &common{state: &state.State{DB: &db.DB{Cluster: cluster}}, localConfig: map[string]string{"volatile.uuid": id}}
			require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				networkID, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "cleanup-debt", "", db.NetworkTypeOVN, nil)
				if err != nil {
					return err
				}

				if err = tx.NetworkCreated(api.ProjectDefaultName, "cleanup-debt"); err != nil {
					return err
				}

				if err = tx.NetworkNodeCreated(networkID); err != nil {
					return err
				}

				token := uuid.NewString()
				if err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "cleanup-debt", token, "nic"); err != nil {
					return err
				}

				if name == "no-record" || name == "query-error-retry" {
					return nil
				}

				a := db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: cluster.GetNodeID(), InstanceUUID: id, DeviceName: "eth0", Version: 1, NetworkIDs: []int64{networkID}, Payload: "{}"}
				if name == "other-instance" {
					a.InstanceUUID = uuid.NewString()
				}

				tokens := map[int64]string{networkID: token}
				if err = tx.CaptureOVNNICCleanup(ctx, a, tokens); err != nil {
					return err
				}

				if name == "completed" {
					return tx.CompleteOVNNICCleanup(ctx, a, tokens)
				}

				return nil
			}))
			if name == "query-error-retry" {
				require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "ALTER TABLE networks_ovn_nic_cleanup RENAME TO injected_hidden_cleanup")
					return err
				}))
			}

			err := d.ensureOVNStopCleanupComplete()
			switch name {
			case "pending":
				require.ErrorContains(t, err, "unacknowledged")
			case "query-error-retry":
				require.Error(t, err)
				require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "ALTER TABLE injected_hidden_cleanup RENAME TO networks_ovn_nic_cleanup")
					return err
				}))
				require.NoError(t, d.ensureOVNStopCleanupComplete())
			default:
				require.NoError(t, err)
			}
		})
	}
}

type sourceCleanupCaptureDevice struct {
	device.Device
	capture func() error
}

func (d *sourceCleanupCaptureDevice) OVNStopCleanupCapture() error {
	return d.capture()
}

func TestDriverOVNStopCaptureBeforeDispatch(t *testing.T) {
	for _, name := range []string{"success", "first-error", "second-error", "load-error", "nil-device", "unsupported", "usable-validation-warning", "non-ovn", "no-managed-nic"} {
		t.Run(name, func(t *testing.T) {
			d := &common{logger: logger.AddContext(logger.Ctx{})}
			firstErr, secondErr, loadErr := errors.New("first publication"), errors.New("second publication"), errors.New("load")
			entries := []deviceConfig.DeviceNamed{
				{Name: "disk", Config: deviceConfig.Device{"type": "disk"}},
				{Name: "unmanaged", Config: deviceConfig.Device{"type": "nic"}},
				{Name: "first", Config: deviceConfig.Device{"type": "nic", "network": "source"}},
				{Name: "second", Config: deviceConfig.Device{"type": "nic", "network": "source"}},
			}

			if name == "no-managed-nic" {
				entries = entries[:2]
			}

			var events []string
			err := d.captureOVNStopDevices(entries, func(entry deviceConfig.DeviceNamed) (device.Device, error) {
				events = append(events, "load-"+entry.Name)
				if entry.Name == "first" {
					switch name {
					case "load-error":
						return nil, loadErr
					case "nil-device":
						return nil, nil
					case "unsupported":
						return nil, device.ErrUnsupportedDevType
					case "non-ovn":
						return &stoppedCleanupDevice{}, nil
					}
				}

				dev := &sourceCleanupCaptureDevice{capture: func() error {
					events = append(events, "capture-"+entry.Name)
					if entry.Name == "first" && name == "first-error" {
						return firstErr
					}

					if entry.Name == "second" && name == "second-error" {
						return secondErr
					}

					return nil
				}}
				if entry.Name == "first" && name == "usable-validation-warning" {
					return dev, loadErr
				}

				return dev, nil
			})
			switch name {
			case "first-error":
				require.ErrorIs(t, err, firstErr)
				require.Equal(t, []string{"load-first", "capture-first"}, events)
			case "second-error":
				require.ErrorIs(t, err, secondErr)
				require.Equal(t, []string{"load-first", "capture-first", "load-second", "capture-second"}, events)
			case "load-error":
				require.ErrorIs(t, err, loadErr)
				require.Equal(t, []string{"load-first"}, events)
			case "nil-device":
				require.ErrorContains(t, err, "no usable device")
				require.Equal(t, []string{"load-first"}, events)
			case "unsupported", "non-ovn":
				require.NoError(t, err)
				require.Equal(t, []string{"load-first", "load-second", "capture-second"}, events)
			case "no-managed-nic":
				require.NoError(t, err)
				require.Empty(t, events)
			default:
				require.NoError(t, err)
				require.Equal(t, []string{"load-first", "capture-first", "load-second", "capture-second"}, events)
			}
		})
	}
}

func TestDriverOVNStopPartialCaptureRetryKeepsDebt(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	id, token := uuid.NewString(), uuid.NewString()
	var networkID int64
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		networkID, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "source-handoff", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		if err = tx.NetworkCreated(api.ProjectDefaultName, "source-handoff"); err != nil {
			return err
		}

		if err = tx.NetworkNodeCreated(networkID); err != nil {
			return err
		}

		return tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "source-handoff", token, "nic")
	}))
	attempts := map[string]db.OVNNICCleanup{}
	for _, name := range []string{"first", "second"} {
		attempts[name] = db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: cluster.GetNodeID(), InstanceUUID: id, DeviceName: name, Version: 1, NetworkIDs: []int64{networkID}, Payload: "{\"original-source\":42}"}
	}

	d := &common{logger: logger.AddContext(logger.Ctx{}), state: &state.State{DB: &db.DB{Cluster: cluster}}, localConfig: map[string]string{"volatile.uuid": id}}
	entries := []deviceConfig.DeviceNamed{
		{Name: "first", Config: deviceConfig.Device{"type": "nic", "network": "source-handoff"}},
		{Name: "second", Config: deviceConfig.Device{"type": "nic", "network": "source-handoff"}},
	}

	publicationErr := errors.New("second publication refused")
	failSecond := true
	load := func(entry deviceConfig.DeviceNamed) (device.Device, error) {
		return &sourceCleanupCaptureDevice{capture: func() error {
			if entry.Name == "second" && failSecond {
				return publicationErr
			}

			return cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.CaptureOVNNICCleanup(ctx, attempts[entry.Name], map[int64]string{networkID: token})
			})
		}}, nil
	}

	require.ErrorIs(t, d.captureOVNStopDevices(entries, load), publicationErr)
	require.ErrorContains(t, d.ensureOVNStopCleanupComplete(), "unacknowledged")
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		first, err := tx.OVNNICCleanupByGeneration(ctx, attempts["first"].Generation)
		if err != nil {
			return err
		}

		require.Equal(t, attempts["first"], first)
		require.ErrorContains(t, tx.EnsureOVNNICCleanupComplete(ctx, networkID), "unacknowledged")
		return nil
	}))
	failSecond = false
	require.NoError(t, d.captureOVNStopDevices(entries, load))
	// Publishing both original inputs still acknowledges no external effect.
	require.ErrorContains(t, d.ensureOVNStopCleanupComplete(), "unacknowledged")
	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for name, expected := range attempts {
			actual, err := tx.OVNNICCleanupByGeneration(ctx, expected.Generation)
			if err != nil {
				return err
			}

			require.Equal(t, expected, actual, name)
		}

		return nil
	}))
}

type sourceReplayNetwork struct {
	network.Network
	id int64
}

func (n *sourceReplayNetwork) ID() int64 { return n.id }

type sourceReplayDevice struct {
	device.Device
	name       string
	n          network.Network
	generation string
}

func (d *sourceReplayDevice) Name() string                { return d.name }
func (d *sourceReplayDevice) OVNNetwork() network.Network { return d.n }
func (d *sourceReplayDevice) OVNStopCleanupSelectGeneration(generation string) {
	d.generation = generation
}

// Only the private cluster constructor runs. No device factory, operation lock,
// driver Stop/status, host, QMP or liblxc call is executed.
func sourceReplayFixture(t *testing.T) (*common, []db.OVNNICCleanup) {
	t.Helper()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	d := &common{state: &state.State{DB: &db.DB{Cluster: cluster}, ServerName: "source-member"}, logger: logger.AddContext(logger.Ctx{}), localConfig: map[string]string{"volatile.uuid": uuid.NewString()}}
	var sources []db.OVNNICCleanup
	for _, name := range []string{"removed", "changed"} {
		payload := map[string]any{
			"Kind": "incus-ovn-nic-stop", "Version": 1, "NetworkID": int64(41), "InstanceID": 42,
			"DeviceConfig": deviceConfig.Device{"type": "nic", "network": "original-network"},
			"HostVolatile": map[string]string{"host_name": "original-host"},
			"OVS":          &ovs.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), BridgeUUID: uuid.NewString(), BridgeName: "br-original", PortUUID: uuid.NewString(), InterfaceUUID: uuid.NewString(), InterfaceName: "original-host", OVNPortName: "original-port"},
			"Port":         networkOVN.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), SwitchUUID: uuid.NewString(), PortUUID: uuid.NewString(), PortVersion: uuid.NewString(), SwitchName: "original-switch", PortName: "original-port", Source: d.state.ServerName},
		}

		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		sources = append(sources, db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: cluster.GetNodeID(), InstanceUUID: d.localConfig["volatile.uuid"], DeviceName: name, Version: 1, NetworkIDs: []int64{41, 99}, Payload: string(raw)})
	}

	d.expandedDevices = deviceConfig.Devices{"changed": {"type": "nic", "network": "replacement-network"}}
	return d, sources
}

func TestDriverOVNStoppedSourceReplay(t *testing.T) {
	for _, name := range []string{"success", "first-error", "both-errors", "load-error", "unsupported", "usable-unsupported", "nil-device", "validation-warning", "wrong-network", "inherited-network-is-not-primary", "missing-network", "not-ovn", "wrong-name", "wrong-instance", "bad-payload"} {
		t.Run(name, func(t *testing.T) {
			d, sources := sourceReplayFixture(t)
			firstErr, secondErr, loadErr := errors.New("first effect"), errors.New("second effect"), errors.New("load")
			if name == "wrong-instance" {
				sources[0].InstanceUUID = uuid.NewString()
			}

			if name == "bad-payload" {
				sources[0].Payload = "{}"
			}

			var loads, stops []string
			err := d.retryOriginalOVNStopDevices(sources, func(source *network.OVNInstanceNICStopOpts) (device.Device, error) {
				loads = append(loads, source.DeviceName)
				require.Equal(t, "original-network", source.DeviceConfig["network"])
				require.Equal(t, "original-host", source.HostVolatile["host_name"])
				require.Equal(t, 42, source.InstanceID)
				dev := &sourceReplayDevice{name: source.DeviceName, n: &sourceReplayNetwork{id: 41}}
				if source.DeviceName == "removed" {
					switch name {
					case "load-error":
						return nil, loadErr
					case "unsupported":
						return nil, device.ErrUnsupportedDevType
					case "usable-unsupported":
						return dev, device.ErrUnsupportedDevType
					case "nil-device":
						return nil, nil
					case "validation-warning":
						return dev, loadErr
					case "wrong-network":
						dev.n = &sourceReplayNetwork{id: 42}
					case "inherited-network-is-not-primary":
						dev.n = &sourceReplayNetwork{id: 99}
					case "missing-network":
						dev.n = nil
					case "not-ovn":
						return &stoppedCleanupDevice{name: source.DeviceName}, nil
					case "wrong-name":
						dev.name = "replacement-name"
					}
				}

				return dev, nil
			}, func(dev device.Device) error {
				stops = append(stops, dev.Name())
				selected, ok := dev.(*sourceReplayDevice)
				require.True(t, ok)
				require.NotEmpty(t, selected.generation)
				for _, a := range sources {
					if a.DeviceName == dev.Name() {
						require.Equal(t, a.Generation, selected.generation)
					}
				}

				if dev.Name() == "removed" && (name == "first-error" || name == "both-errors") {
					return firstErr
				}

				if dev.Name() == "changed" && name == "both-errors" {
					return secondErr
				}

				return nil
			})
			switch name {
			case "success", "validation-warning":
				require.NoError(t, err)
			case "first-error":
				require.ErrorIs(t, err, firstErr)
			case "both-errors":
				require.ErrorIs(t, err, firstErr)
				require.ErrorIs(t, err, secondErr)
			case "load-error":
				require.ErrorIs(t, err, loadErr)
			default:
				require.Error(t, err)
			}

			if name == "success" || name == "validation-warning" || name == "first-error" || name == "both-errors" {
				require.Equal(t, []string{"removed", "changed"}, stops)
			} else {
				require.Equal(t, []string{"changed"}, stops)
			}

			if name == "wrong-instance" || name == "bad-payload" {
				require.Equal(t, []string{"changed"}, loads)
			} else {
				require.Equal(t, []string{"removed", "changed"}, loads)
			}
		})
	}
}

func TestDriverOVNStoppedDebtEnumeration(t *testing.T) {
	d, _ := sourceReplayFixture(t)
	var networkID int64
	token := uuid.NewString()
	require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		networkID, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "original-debt", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		if err = tx.NetworkCreated(api.ProjectDefaultName, "original-debt"); err != nil {
			return err
		}

		if err = tx.NetworkNodeCreated(networkID); err != nil {
			return err
		}

		return tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "original-debt", token, "nic")
	}))
	ours := db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: d.state.DB.Cluster.GetNodeID(), InstanceUUID: d.localConfig["volatile.uuid"], DeviceName: "removed", Version: 1, NetworkIDs: []int64{networkID}, Payload: "{\"original\":42}"}
	other := ours
	other.Generation, other.InstanceUUID = uuid.NewString(), uuid.NewString()
	require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, a := range []db.OVNNICCleanup{ours, other} {
			err := tx.CaptureOVNNICCleanup(ctx, a, map[int64]string{networkID: token})
			if err != nil {
				return err
			}
		}

		return nil
	}))
	got, err := d.pendingOVNStopSources()
	require.NoError(t, err)
	require.Equal(t, []db.OVNNICCleanup{ours}, got)
	require.Error(t, d.ensureOVNStopCleanupComplete())
	require.NoError(t, d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CompleteOVNNICCleanup(ctx, ours, map[int64]string{networkID: token})
	}))
	got, err = d.pendingOVNStopSources()
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, d.ensureOVNStopCleanupComplete())
	d.localConfig["volatile.uuid"] = uuid.NewString()
	got, err = d.pendingOVNStopSources()
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, d.state.DB.Cluster.Close())
	_, err = d.pendingOVNStopSources()
	require.Error(t, err)
}

func TestDriverOVNRetiredVolatileMemory(t *testing.T) {
	for _, name := range []string{"matching", "already-empty", "wrong-uuid", "changed-local", "changed-expanded"} {
		t.Run(name, func(t *testing.T) {
			id := uuid.NewString()
			d := &common{localConfig: map[string]string{"volatile.uuid": id, "volatile.eth0.host_name": "original", "volatile.sibling.host_name": "sibling"}, expandedConfig: map[string]string{"volatile.eth0.host_name": "original", "user.keep": "unrelated"}}
			original := map[string]string{"host_name": "original"}
			if name == "already-empty" {
				delete(d.localConfig, "volatile.eth0.host_name")
				delete(d.expandedConfig, "volatile.eth0.host_name")
			}

			if name == "wrong-uuid" {
				id = uuid.NewString()
			}

			if name == "changed-local" {
				d.localConfig["volatile.eth0.last_state.pci.driver"] = "replacement"
			}

			if name == "changed-expanded" {
				d.expandedConfig["volatile.eth0.last_state.pci.driver"] = "replacement"
			}

			beforeLocal, beforeExpanded := maps.Clone(d.localConfig), maps.Clone(d.expandedConfig)
			err := d.OVNStopCleanupVolatileRetired(id, "eth0", original)
			if name == "matching" || name == "already-empty" {
				require.NoError(t, err)
				require.NotContains(t, d.localConfig, "volatile.eth0.host_name")
				require.NotContains(t, d.expandedConfig, "volatile.eth0.host_name")
				require.Equal(t, "sibling", d.localConfig["volatile.sibling.host_name"])
				require.Equal(t, "unrelated", d.expandedConfig["user.keep"])
			} else {
				require.Error(t, err)
				require.Equal(t, beforeLocal, d.localConfig)
				require.Equal(t, beforeExpanded, d.expandedConfig)
			}
		})
	}
}

// These are the private production result boundaries used by the real callers.
// All continuations are counted fake effects: no driver/operation/QMP/CRIU/DB.
func TestMigrationHandoverStopCleanupResult(t *testing.T) {
	for _, name := range []string{"success", "stop-error", "force-error", "both-errors", "already-stopped", "already-stopped-debt", "success-debt", "stop-and-debt"} {
		t.Run(name, func(t *testing.T) {
			stopFailure, forceFailure, debt := errors.New("stop cleanup"), errors.New("force stop"), errors.New("pending source")
			var stopErr, forceErr, debtErr error
			switch name {
			case "stop-error", "both-errors", "stop-and-debt":
				stopErr = stopFailure
			case "force-error":
				stopErr, forceErr = stopFailure, forceFailure
			case "already-stopped", "already-stopped-debt":
				stopErr = ErrInstanceIsStopped
			}

			if name == "both-errors" {
				forceErr = forceFailure
			}

			if name == "already-stopped-debt" || name == "success-debt" || name == "stop-and-debt" {
				debtErr = debt
			}

			var calls []string
			err := stopAfterMigrationHandover(
				func() error { calls = append(calls, "stop"); return stopErr },
				func() error { calls = append(calls, "force"); return forceErr },
				func() error { calls = append(calls, "verify"); return debtErr },
			)
			expected := []string{"stop", "verify"}
			if stopErr != nil {
				expected = []string{"stop", "force", "verify"}
			}

			require.Equal(t, expected, calls)
			require.Equal(t, stopErr == stopFailure, errors.Is(err, stopFailure))
			require.Equal(t, forceErr != nil, errors.Is(err, forceFailure))
			require.Equal(t, debtErr != nil, errors.Is(err, debt))
			if stopErr == nil && forceErr == nil && debtErr == nil || name == "already-stopped" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMigrationSendSourceResult(t *testing.T) {
	for _, name := range []string{"uncommitted-success", "uncommitted-transfer-error", "committed-success", "committed-transfer-error", "committed-cleanup-error", "committed-both-errors"} {
		t.Run(name, func(t *testing.T) {
			transferFailure, cleanupFailure := errors.New("transfer"), errors.New("cleanup")
			committed := name != "uncommitted-success" && name != "uncommitted-transfer-error"
			var transferErr, cleanupErr error
			if name == "uncommitted-transfer-error" || name == "committed-transfer-error" || name == "committed-both-errors" {
				transferErr = transferFailure
			}

			if name == "committed-cleanup-error" || name == "committed-both-errors" {
				cleanupErr = cleanupFailure
			}

			released := 0
			err := migrationSendSourceResult(committed, transferErr, cleanupErr, func() { released++ })
			switch name {
			case "uncommitted-transfer-error":
				require.ErrorIs(t, err, transferFailure)
				require.Zero(t, released)
			default:
				require.Equal(t, 1, released)
				require.Equal(t, cleanupErr != nil, errors.Is(err, cleanupFailure))
				if cleanupErr == nil {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestStatefulStopCleanupResult(t *testing.T) {
	for _, name := range []string{"success", "metadata-error", "running-error", "stopped-error", "stopped-both-errors", "retry-success"} {
		t.Run(name, func(t *testing.T) {
			waitFailure, metadataFailure := errors.New("teardown"), errors.New("metadata")
			var waitErr, metadataErr error
			if name == "running-error" || name == "stopped-error" || name == "stopped-both-errors" {
				waitErr = waitFailure
			}

			if name == "metadata-error" || name == "stopped-both-errors" {
				metadataErr = metadataFailure
			}

			var calls []string
			err := statefulStopCleanupResult(waitErr,
				func() bool { calls = append(calls, "running"); return name == "running-error" },
				func() error { calls = append(calls, "persist"); return metadataErr })
			if name == "running-error" {
				require.Equal(t, []string{"running"}, calls)
			} else if waitErr != nil {
				require.Equal(t, []string{"running", "persist"}, calls)
			} else {
				require.Equal(t, []string{"persist"}, calls)
			}

			require.Equal(t, waitErr != nil, errors.Is(err, waitFailure))
			require.Equal(t, metadataErr != nil, errors.Is(err, metadataFailure))
			if waitErr == nil && metadataErr == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// Buffered protocol channels and fake verification exercise the actual private
// production completion seam, without CRIU, live migration or stop hooks.
func TestMigrationDumpCleanupResult(t *testing.T) {
	for _, name := range []string{"nonlive-success", "nonlive-error", "live-transfer-error", "async-transfer-error", "sync-success", "sync-debt", "async-success", "async-debt", "async-dump-error", "async-both-errors", "collected-success"} {
		t.Run(name, func(t *testing.T) {
			transferFailure, dumpFailure, debtFailure := errors.New("transfer"), errors.New("final dump"), errors.New("source debt")
			live := name != "nonlive-success" && name != "nonlive-error"
			async := name == "async-transfer-error" || name == "async-success" || name == "async-debt" || name == "async-dump-error" || name == "async-both-errors"
			var transferErr, dumpErr, debtErr error
			if name == "nonlive-error" || name == "live-transfer-error" || name == "async-transfer-error" {
				transferErr = transferFailure
			}

			if name == "async-dump-error" || name == "async-both-errors" {
				dumpErr = dumpFailure
			}

			if name == "sync-debt" || name == "async-debt" || name == "async-both-errors" {
				debtErr = debtFailure
			}

			restore, dump := make(chan bool, 1), make(chan error, 1)
			dump <- dumpErr
			if name == "collected-success" {
				dump = nil
			}

			verified := 0
			err := migrationDumpCleanupResult(live, async, transferErr, restore, dump, func() error {
				verified++
				require.Len(t, restore, 1)
				require.True(t, <-restore)
				if async {
					require.Empty(t, dump)
				} else {
					switch name {
					case "collected-success":
						require.Nil(t, dump)
					default:
						require.Len(t, dump, 1)
					}
				}

				return debtErr
			})
			if !live {
				require.Empty(t, restore)
				require.Len(t, dump, 1)
				require.Zero(t, verified)
			} else if transferErr != nil {
				require.False(t, <-restore)
				require.Len(t, dump, 1)
				require.Zero(t, verified)
			} else {
				require.Equal(t, 1, verified)
			}

			require.Equal(t, transferErr != nil, errors.Is(err, transferFailure))
			require.Equal(t, dumpErr != nil, errors.Is(err, dumpFailure))
			require.Equal(t, debtErr != nil, errors.Is(err, debtFailure))
			if transferErr == nil && dumpErr == nil && debtErr == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestShutdownAfterPowerdownCleanupOwnership(t *testing.T) {
	requestFailure := errors.New("powerdown request")
	cleanupFailure := errors.New("hook cleanup")
	for _, name := range []string{"ordinary-success", "ordinary-cleanup-error", "disconnected-success", "disconnected-cleanup-error", "wrapped-disconnect-cleanup-error", "request-error"} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			var requestErr, waitErr error
			switch name {
			case "disconnected-success", "disconnected-cleanup-error":
				requestErr = qmp.ErrMonitorDisconnect
			case "wrapped-disconnect-cleanup-error":
				requestErr = fmt.Errorf("powerdown: %w", qmp.ErrMonitorDisconnect)
			case "request-error":
				requestErr = requestFailure
			}

			if strings.Contains(name, "cleanup-error") {
				waitErr = cleanupFailure
			}

			err := shutdownAfterPowerdown(requestErr, func(e error) {
				calls = append(calls, "failed-request")
				require.ErrorIs(t, e, requestFailure)
			}, func() { calls = append(calls, "repeat") }, func() error {
				calls = append(calls, "wait-cleanup")
				return waitErr
			})
			switch name {
			case "request-error":
				require.Equal(t, []string{"failed-request"}, calls)
				require.ErrorIs(t, err, requestFailure)
			default:
				expected := []string{"wait-cleanup"}
				if requestErr == nil {
					expected = []string{"repeat", "wait-cleanup"}
				}

				require.Equal(t, expected, calls)
				if waitErr != nil {
					require.ErrorIs(t, err, cleanupFailure)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestShutdownCleanupResult(t *testing.T) {
	for _, name := range []string{"stopped-success", "stopped-pending-debt", "stopped-query-failure", "stopped-wait-failure", "running-successful-wait", "running-failed-wait", "frozen", "error-status"} {
		t.Run(name, func(t *testing.T) {
			waitFailure := errors.New("shutdown hook wait")
			debtFailure := errors.New("pending original NIC cleanup")
			queryFailure := errors.New("cleanup database query")
			status := api.Stopped
			var waitErr, verifyErr error
			switch name {
			case "stopped-pending-debt":
				verifyErr = debtFailure
			case "stopped-query-failure":
				verifyErr = queryFailure
			case "stopped-wait-failure":
				waitErr = waitFailure
			case "running-successful-wait":
				status = api.Running
			case "running-failed-wait":
				status, waitErr = api.Running, waitFailure
			case "frozen":
				status = api.Frozen
			case "error-status":
				status = api.Error
			}

			queries := 0
			err := shutdownCleanupResult(waitErr, status, func() error { queries++; return verifyErr })
			if status != api.Stopped {
				require.ErrorContains(t, err, fmt.Sprintf("status is %q", status))
			} else if waitErr != nil {
				require.ErrorIs(t, err, waitFailure)
			} else if verifyErr != nil {
				require.ErrorIs(t, err, verifyErr)
			} else {
				require.NoError(t, err)
			}

			if waitErr != nil {
				require.ErrorIs(t, err, waitFailure)
			}

			expectedQueries := 0
			if status == api.Stopped && waitErr == nil {
				expectedQueries = 1
			}

			require.Equal(t, expectedQueries, queries)
		})
	}

	t.Run("fresh-success-after-pending-refusal", func(t *testing.T) {
		debtFailure := errors.New("pending original NIC cleanup")
		require.ErrorIs(t, shutdownCleanupResult(nil, api.Stopped, func() error { return debtFailure }), debtFailure)
		require.NoError(t, shutdownCleanupResult(nil, api.Stopped, func() error { return nil }))
	})
}

type liveAcknowledgmentDevice struct {
	stoppedCleanupDevice
	complete func() error
}

func (*liveAcknowledgmentDevice) CanHotPlug() bool { return true }
func (*liveAcknowledgmentDevice) Config() deviceConfig.Device {
	return deviceConfig.Device{"type": "none"}
}

func (d *liveAcknowledgmentDevice) OVNStopCleanupComplete() error { return d.complete() }

func TestDriverOVNLiveDetachAcknowledgment(t *testing.T) {
	for _, driver := range []string{"lxc", "qemu"} {
		for _, phase := range []string{"success", "hook-error", "completion-error", "whole-instance-stop"} {
			t.Run(driver+"/"+phase, func(t *testing.T) {
				hookErr, commitErr := errors.New("original host hook failed"), errors.New("terminal commit failed")
				completions := 0
				dev := &liveAcknowledgmentDevice{stoppedCleanupDevice: stoppedCleanupDevice{name: "original"}, complete: func() error {
					completions++
					if phase == "completion-error" {
						return commitErr
					}

					return nil
				}}
				if phase == "hook-error" {
					dev.hookErr = hookErr
				}

				d := common{logger: logger.AddContext(logger.Ctx{})}
				running := phase != "whole-instance-stop"
				var err error
				switch driver {
				case "lxc":
					err = (&lxc{common: d}).deviceStop(dev, running, "")
				default:
					err = (&qemu{common: d}).deviceStop(dev, running, "")
				}

				switch phase {
				case "hook-error":
					require.ErrorIs(t, err, hookErr)
					require.Zero(t, completions)
				case "completion-error":
					require.ErrorIs(t, err, commitErr)
					require.Equal(t, 1, completions)
				default:
					require.NoError(t, err)
					if running {
						require.Equal(t, 1, completions)
					} else {
						require.Zero(t, completions)
					}
				}
			})
		}
	}
}

func TestDriverOVNPhysicalPreclaimResetAndStoppedAdmission(t *testing.T) {
	for _, mode := range []string{"persisted-marker-stale-memory", "ordinary-no-marker"} {
		t.Run(mode, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			defer cleanup()
			ctx := context.Background()
			id := 0
			instanceUUID := uuid.NewString()
			var original string
			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'partial-start',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
				if err != nil {
					return err
				}

				n, err := res.LastInsertId()
				if err != nil {
					return err
				}

				id = int(n)
				raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "SourceNodeID": tx.GetNodeID(), "InstanceUUID": instanceUUID, "InstanceID": id, "DeviceName": "eth0"})
				if err != nil {
					return err
				}

				original = string(raw)
				values := map[string]string{"volatile.uuid": instanceUUID, "volatile.eth0.host_name": "original-vf"}
				if mode == "persisted-marker-stale-memory" {
					values["volatile.eth0.last_state.ovn.physical"] = original
				}

				for key, value := range values {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
					if err != nil {
						return err
					}
				}

				return nil
			}))
			d := &common{id: id, state: &state.State{DB: &db.DB{Cluster: cluster}}, localConfig: map[string]string{"volatile.uuid": instanceUUID, "volatile.eth0.host_name": "original-vf"}, expandedConfig: map[string]string{"volatile.uuid": instanceUUID, "volatile.eth0.host_name": "original-vf"}}
			err := d.deviceVolatileReset("eth0", deviceConfig.Device{"type": "disk"}, nil)
			switch mode {
			case "persisted-marker-stale-memory":
				require.ErrorContains(t, err, "cannot be erased")
				err = d.retryStoppedOVNStop(nil, func() api.StatusCode { return api.Stopped }, func(device.Device) error { t.Fatal("no-row retry may not select Desired"); return nil }, func() error { t.Fatal("unproven partial claim cannot acknowledge terminal cleanup"); return nil })
				require.ErrorContains(t, err, "quarantined")
				require.Equal(t, "original-vf", d.localConfig["volatile.eth0.host_name"])
				require.ErrorContains(t, (&lxc{common: *d}).delete(true, true), "quarantined")
				require.ErrorContains(t, (&qemu{common: *d}).delete(true, true), "quarantined")
				// The actual stopped Restore entry points must refuse before
				// touching a source snapshot, operation, storage or config.
				// An existing Start operation makes both status paths return
				// Stopped without accessing liblxc, QMP or host process state.
				d.name = "preclaim-fixture-" + instanceUUID
				d.project = api.Project{Name: "default"}
				op, err := operationlock.Create(d.Project().Name, d.Name(), nil, operationlock.ActionStart, false, false)
				require.NoError(t, err)
				defer op.Done(nil)
				require.ErrorContains(t, (&lxc{common: *d}).Restore(nil, false, false), "quarantined")
				require.ErrorContains(t, (&qemu{common: *d}).Restore(nil, false, false), "quarantined")
				require.ErrorContains(t, (&lxc{common: *d}).Restore(nil, false, true), "quarantined")
				require.ErrorContains(t, (&qemu{common: *d}).Restore(nil, false, true), "quarantined")
			default:
				require.NoError(t, err)
				require.Empty(t, d.localConfig["volatile.eth0.host_name"])
			}

			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var host, marker string
				err := tx.Tx().QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'),''), COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.physical'),'')", id, id).Scan(&host, &marker)
				switch mode {
				case "persisted-marker-stale-memory":
					require.Equal(t, "original-vf", host)
					require.Equal(t, original, marker)
				default:
					require.Empty(t, host)
					require.Empty(t, marker)
				}

				return err
			}))
		})
	}
}

func TestDriverOVNSnapshotAllocationNormalization(t *testing.T) {
	source := map[string]string{"volatile.uuid": uuid.NewString(), "volatile.uuid.generation": uuid.NewString(), "volatile.eth0.last_state.ovn.host": "original-run", "volatile.vf.last_state.ovn.physical": "original-VF", "volatile.disk.host_name": "unrelated", "volatile.ordinary.host_name": "ordinary-NIC", "volatile.eth0.hwaddr": "guest-MAC", "volatile.eth0.name": "guest-name", "volatile.eth0.last_state.ip_addresses": "guest-IP", "user.keep": "unrelated"}
	for _, name := range []string{"eth0", "vf", "explicit"} {
		for _, key := range db.OVNNICStopVolatileKeys() {
			if name == "explicit" && (key == "last_state.ovn.host" || key == "last_state.ovn.physical") {
				continue
			}

			if source["volatile."+name+"."+key] == "" {
				source["volatile."+name+"."+key] = "original-allocation"
			}
		}
	}

	before := maps.Clone(source)
	result := normalizeOVNSnapshotConfig(source, deviceConfig.Devices{"explicit": {"type": "nic", "nictype": "ovn"}, "disk": {"type": "disk"}, "ordinary": {"type": "nic", "nictype": "bridged"}})
	for _, name := range []string{"eth0", "vf", "explicit"} {
		for _, key := range db.OVNNICStopVolatileKeys() {
			require.NotContains(t, result, "volatile."+name+"."+key)
		}
	}

	for _, key := range []string{"volatile.uuid", "volatile.uuid.generation", "volatile.disk.host_name", "volatile.ordinary.host_name", "volatile.eth0.hwaddr", "volatile.eth0.name", "volatile.eth0.last_state.ip_addresses", "user.keep"} {
		require.Equal(t, before[key], result[key], key)
	}

	require.Equal(t, before, source, "the original snapshot's allocation evidence remains intact")
}
