package device

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/bgp"
	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type nicStopCaptureInstance struct{ instance.Instance }

func (*nicStopCaptureInstance) ID() int { return 19 }
func (*nicStopCaptureInstance) LocalConfig() map[string]string {
	return map[string]string{"volatile.uuid": "2a981b87-9238-46cb-8e8d-ac766142a773"}
}

type nicStopCaptureNetwork struct {
	ovnNet
	t           *testing.T
	capture     func(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error
	stop        func() error
	source      func(context.Context, string, string) (*network.OVNInstanceNICStopOpts, error)
	captured    *network.OVNInstanceNICStopOpts
	stopOptions func(*network.OVNInstanceNICStopOpts) error
	retire      func(context.Context, string, string, string) (bool, error)
}

func (n *nicStopCaptureNetwork) InstanceDevicePortStopCapture(port ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error {
	err := n.capture(port, opts)
	if err == nil {
		cloned := *opts
		cloned.DeviceConfig = opts.DeviceConfig.Clone()
		cloned.HostVolatile = maps.Clone(opts.HostVolatile)
		cloned.CleanupGeneration = "b2500167-d799-426f-8b32-c99b2451918a"
		n.captured = &cloned
	}

	return err
}

func (n *nicStopCaptureNetwork) InstanceDevicePortStopSource(ctx context.Context, instanceUUID string, deviceName string) (*network.OVNInstanceNICStopOpts, error) {
	if n.source != nil {
		return n.source(ctx, instanceUUID, deviceName)
	}

	require.NotNil(n.t, ctx)
	if n.captured == nil {
		return nil, sql.ErrNoRows
	}

	require.Equal(n.t, instanceUUID, n.captured.InstanceUUID)
	require.Equal(n.t, deviceName, n.captured.DeviceName)
	return n.captured, nil
}

func (n *nicStopCaptureNetwork) InstanceDevicePortStop(_ ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error {
	if n.stopOptions != nil {
		return n.stopOptions(opts)
	}

	if n.stop != nil {
		return n.stop()
	}

	n.t.Fatal("network cleanup continuation entered after source capture refusal")
	return nil
}

func (n *nicStopCaptureNetwork) InstanceDevicePortStopRetire(ctx context.Context, id, dev, generation string) (bool, error) {
	require.NotNil(n.t, n.retire)
	return n.retire(ctx, id, dev, generation)
}

func TestNICOVNStopCaptureRefusalBeforeCleanup(t *testing.T) {
	for _, tc := range []struct{ name, address4, address6, want4, want6 string }{
		{name: "unaddressed"},
		{name: "ipv4-cidr", address4: "192.0.2.2/24", want4: "192.0.2.2"},
		{name: "ipv6-cidr", address6: "2001:db8::2/64", want6: "2001:db8::2"},
		{name: "both", address4: "192.0.2.2", address6: "2001:db8::2", want4: "192.0.2.2", want6: "2001:db8::2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := errors.New("source capture unavailable")
			original := map[string]string{"host_name": "original-host", "hwaddr": "00:16:3e:01:02:03", "last_state.ip_addresses": "192.0.2.2"}
			volatile := maps.Clone(original)
			captured := 0
			ovsReads := 0
			d := &nicOVN{deviceCommon: deviceCommon{
				logger:      logger.AddContext(logger.Ctx{}),
				inst:        &nicStopCaptureInstance{},
				name:        "eth0",
				config:      deviceConfig.Device{"network": "original-network", "nested": "parent", "ipv4.address": tc.address4, "ipv6.address": tc.address6},
				state:       &state.State{ShutdownCtx: context.Background(), OVS: func() (*ovs.VSwitch, error) { ovsReads++; return nil, errors.New("inert OVS unavailable") }},
				volatileGet: func() map[string]string { return volatile },
				volatileSet: func(map[string]string) error {
					t.Fatal("volatile clearing entered after source capture refusal")
					return nil
				},
			}}
			d.network = &nicStopCaptureNetwork{t: t, capture: func(port ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error {
				captured++
				require.Empty(t, port)
				require.Equal(t, 19, opts.InstanceID)
				require.Equal(t, "2a981b87-9238-46cb-8e8d-ac766142a773", opts.InstanceUUID)
				require.Equal(t, "eth0", opts.DeviceName)
				require.Equal(t, original, opts.HostVolatile)
				require.Equal(t, "original-host", opts.DeviceConfig["host_name"])
				require.Equal(t, original["hwaddr"], opts.DeviceConfig["hwaddr"])
				require.Equal(t, tc.want4, opts.DeviceConfig["ipv4.address"])
				require.Equal(t, tc.want6, opts.DeviceConfig["ipv6.address"])
				return sentinel
			}}
			runConf, err := d.Stop()
			require.ErrorIs(t, err, sentinel)
			require.ErrorContains(t, err, "before host detach")
			require.Nil(t, runConf)
			require.Equal(t, 1, captured)
			require.Zero(t, ovsReads)
			require.Equal(t, original, volatile)
			require.Nil(t, d.state.BGP) // The actual Stop must not enter BGP or expose postStop hooks.
		})
	}
}

func TestNICOVNStopCleanupErrorReachesHook(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		networkFailure, ovsFailure, clearFailure bool
	}{
		{name: "success-clears-after-hook"},
		{name: "network-failure-retains", networkFailure: true},
		{name: "ovs-unavailable-retains", ovsFailure: true},
		{name: "both-causes-retained", networkFailure: true, ovsFailure: true},
		{name: "clear-write-failure-visible", clearFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			networkErr := errors.New("original network cleanup failed")
			ovsErr := errors.New("OVS cleanup unavailable")
			clearErr := errors.New("volatile write failed")
			original := map[string]string{"last_state.created": "true"}
			volatile := maps.Clone(original)
			captures, stops, clears := 0, 0, 0
			d := &nicOVN{deviceCommon: deviceCommon{
				logger: logger.AddContext(logger.Ctx{}), inst: &nicRetirementInstance{Instance: &nicStopCaptureInstance{}, sync: func(id, dev string, values map[string]string) error {
					require.Equal(t, original, values)
					for key := range values {
						volatile[key] = ""
					}

					return nil
				}}, name: "eth0",
				config: deviceConfig.Device{"network": "original-network", "nested": "parent"},
				state: &state.State{ShutdownCtx: context.Background(), BGP: &bgp.Server{}, OVS: func() (*ovs.VSwitch, error) {
					if tc.ovsFailure {
						return nil, ovsErr
					}

					return nil, nil
				}},
				volatileGet: func() map[string]string { return volatile },
				volatileSet: func(map[string]string) error { t.Fatal("Blind volatile setter entered"); return nil },
			}}
			d.network = &nicStopCaptureNetwork{t: t, capture: func(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error { captures++; return nil }, stop: func() error {
				stops++
				if tc.networkFailure {
					return networkErr
				}

				return nil
			}, retire: func(_ context.Context, id, dev, generation string) (bool, error) {
				clears++
				require.Equal(t, "eth0", dev)
				require.NotEmpty(t, generation)
				if tc.clearFailure {
					return false, clearErr
				}

				return true, nil
			}}
			if tc.ovsFailure {
				d.network.(*nicStopCaptureNetwork).source = func(_ context.Context, id, dev string) (*network.OVNInstanceNICStopOpts, error) {
					return &network.OVNInstanceNICStopOpts{
						CleanupGeneration: "b2500167-d799-426f-8b32-c99b2451918a", InstanceUUID: id, InstanceID: 19, DeviceName: dev,
						DeviceConfig: deviceConfig.Device{"network": "original-network"}, HostVolatile: maps.Clone(original), OVS: nicStopOVSPlan(),
					}, nil
				}
			}

			runConf, err := d.Stop()
			require.NoError(t, err)
			require.NotNil(t, runConf)
			require.Len(t, runConf.PostHooks, 1)
			if tc.ovsFailure {
				require.Zero(t, captures)
			} else {
				require.Equal(t, 1, captures)
			}

			require.Equal(t, 1, stops)
			require.Zero(t, clears)
			require.Equal(t, original, volatile)
			err = runConf.PostHooks[0]()
			if tc.networkFailure {
				require.ErrorIs(t, err, networkErr)
			}

			if tc.ovsFailure {
				require.ErrorIs(t, err, ovsErr)
			}

			if tc.networkFailure || tc.ovsFailure {
				require.Zero(t, clears)
				require.Equal(t, original, volatile)
			} else {
				require.Equal(t, 1, clears)
				if tc.clearFailure {
					require.ErrorIs(t, err, clearErr)
					require.Equal(t, original, volatile)
				} else {
					require.NoError(t, err)
					require.Empty(t, volatile["last_state.created"])
				}
			}
		})
	}
}

func TestNICOVNStopCleanupHookKeepsEveryFailure(t *testing.T) {
	for _, tc := range []struct {
		name               string
		prior, host, clear bool
	}{
		{name: "success"},
		{name: "prior", prior: true},
		{name: "host", host: true},
		{name: "prior-and-host", prior: true, host: true},
		{name: "clear", clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			priorErr, hostErr, clearErr := errors.New("prior"), errors.New("host"), errors.New("clear")
			var prior error
			if tc.prior {
				prior = priorErr
			}

			hosts, clears := 0, 0
			hook := nicOVNStopPostHook(prior, func() error {
				hosts++
				if tc.host {
					return hostErr
				}

				return nil
			}, func() error {
				clears++
				if tc.clear {
					return clearErr
				}

				return nil
			})
			err := hook()
			require.Equal(t, 1, hosts)
			if tc.prior {
				require.ErrorIs(t, err, priorErr)
			}

			if tc.host {
				require.ErrorIs(t, err, hostErr)
			}

			if tc.prior || tc.host {
				require.Zero(t, clears)
			} else {
				require.Equal(t, 1, clears)
				if tc.clear {
					require.ErrorIs(t, err, clearErr)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestNICOVNStopVDPALockAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		missing, deleteFail, restoreFail bool
		want                             []string
	}{
		{name: "success", want: []string{"delete", "restore"}},
		{name: "missing-original-name", missing: true},
		{name: "delete-refusal", deleteFail: true, want: []string{"delete"}},
		{name: "restore-refusal", restoreFail: true, want: []string{"delete", "restore"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := map[string]string{"last_state.vdpa.name": "original-vdpa", "last_state.vf.id": "7"}
			if tc.missing {
				delete(original, "last_state.vdpa.name")
			}

			saved := maps.Clone(original)
			deleteErr, restoreErr := errors.New("delete refused"), errors.New("restore refused")
			var events []string
			held := func() {
				if network.SRIOVVirtualFunctionMutex.TryLock() {
					network.SRIOVVirtualFunctionMutex.Unlock()
					t.Fatal("VF lock must remain held across both fake effects")
				}
			}

			err := nicOVNStopVDPA(original, func(name string) error {
				held()
				require.Equal(t, "original-vdpa", name)
				events = append(events, "delete")
				if tc.deleteFail {
					return deleteErr
				}

				return nil
			}, func() error {
				held()
				events = append(events, "restore")
				if tc.restoreFail {
					return restoreErr
				}

				return nil
			})
			require.Equal(t, tc.want, events)
			require.Equal(t, saved, original)
			if tc.missing {
				require.ErrorContains(t, err, "vDPA device")
			} else if tc.deleteFail {
				require.ErrorIs(t, err, deleteErr)
			} else if tc.restoreFail {
				require.ErrorIs(t, err, restoreErr)
			} else {
				require.NoError(t, err)
			}

			require.True(t, network.SRIOVVirtualFunctionMutex.TryLock(), "VF lock must be released on every result")
			network.SRIOVVirtualFunctionMutex.Unlock()
		})
	}

	t.Run("refusal-releases-lock-for-next-attempt", func(t *testing.T) {
		original := map[string]string{"last_state.vdpa.name": "original-vdpa"}
		failed := errors.New("delete refused")
		err := nicOVNStopVDPA(original, func(string) error { return failed }, func() error { t.Fatal("restore after deletion refusal"); return nil })
		require.ErrorIs(t, err, failed)
		deletes, restores := 0, 0
		err = nicOVNStopVDPA(original, func(string) error { deletes++; return nil }, func() error { restores++; return nil })
		require.NoError(t, err)
		require.Equal(t, 1, deletes)
		require.Equal(t, 1, restores)
	})
}

func TestNICOVNStopSourceRefusalBeforeEffects(t *testing.T) {
	for _, tc := range []string{"read-error", "missing", "generation", "instance-id", "instance-uuid", "device-name"} {
		t.Run(tc, func(t *testing.T) {
			readErr := errors.New("original source unavailable")
			reads, captures := 0, 0
			d := &nicOVN{deviceCommon: deviceCommon{
				logger: logger.AddContext(logger.Ctx{}), inst: &nicStopCaptureInstance{}, name: "eth0",
				config:      deviceConfig.Device{"network": "target-network"},
				state:       &state.State{ShutdownCtx: context.Background(), OVS: func() (*ovs.VSwitch, error) { return nil, nil }},
				volatileGet: func() map[string]string { return nil },
				volatileSet: func(map[string]string) error { t.Fatal("volatile write after source refusal"); return nil },
			}}
			d.network = &nicStopCaptureNetwork{
				t:       t,
				capture: func(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error { captures++; return nil },
				source: func(ctx context.Context, id, name string) (*network.OVNInstanceNICStopOpts, error) {
					reads++
					require.Equal(t, d.state.ShutdownCtx, ctx)
					original := &network.OVNInstanceNICStopOpts{CleanupGeneration: "b2500167-d799-426f-8b32-c99b2451918a", InstanceID: 42, InstanceUUID: id, DeviceName: name}
					switch tc {
					case "read-error":
						return original, readErr
					case "missing":
						return nil, nil
					case "generation":
						original.CleanupGeneration = ""
					case "instance-id":
						original.InstanceID = 0
					case "instance-uuid":
						original.InstanceUUID = "replacement"
					case "device-name":
						original.DeviceName = "replacement"
					}

					return original, nil
				},
			}

			runConf, err := d.Stop()
			require.Error(t, err)
			if tc == "read-error" {
				require.ErrorIs(t, err, readErr)
			}

			require.Nil(t, runConf)
			require.Zero(t, captures)
			require.Equal(t, 1, reads)
			require.Nil(t, d.state.BGP)
		})
	}
}

func TestNICOVNStopUsesRecordedSourceInputs(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "network-failure"
		}

		t.Run(name, func(t *testing.T) {
			networkErr := errors.New("network continuation failed")
			original := &network.OVNInstanceNICStopOpts{
				CleanupGeneration: "b2500167-d799-426f-8b32-c99b2451918a",
				InstanceID:        42, InstanceUUID: "2a981b87-9238-46cb-8e8d-ac766142a773", DeviceName: "eth0",
				DeviceConfig: deviceConfig.Device{"network": "source-network", "nested": "parent", "hwaddr": "00:16:3e:01:02:03"},
				HostVolatile: map[string]string{"hwaddr": "00:16:3e:01:02:03", "last_state.created": "true"},
			}

			desired := deviceConfig.Device{"network": "target-network", "hwaddr": "00:16:3e:04:05:06", "host_name": ""}
			currentVolatile := map[string]string{"hwaddr": "00:16:3e:04:05:06"}
			gets, stops, clears, originalCancels, targetCancels := 0, 0, 0, 0, 0
			// Counted callbacks in the existing scan map; no goroutine/neighbor scan.
			instanceNeighborScansMu.Lock()
			priorOriginal, hadOriginal := instanceNeighborScans["instance_42_eth0"]
			priorTarget, hadTarget := instanceNeighborScans["instance_19_eth0"]
			instanceNeighborScans["instance_42_eth0"] = func() { originalCancels++ }
			instanceNeighborScans["instance_19_eth0"] = func() { targetCancels++ }
			instanceNeighborScansMu.Unlock()
			t.Cleanup(func() {
				instanceNeighborScansMu.Lock()
				delete(instanceNeighborScans, "instance_42_eth0")
				delete(instanceNeighborScans, "instance_19_eth0")
				if hadOriginal {
					instanceNeighborScans["instance_42_eth0"] = priorOriginal
				}

				if hadTarget {
					instanceNeighborScans["instance_19_eth0"] = priorTarget
				}

				instanceNeighborScansMu.Unlock()
			})
			d := &nicOVN{deviceCommon: deviceCommon{
				logger: logger.AddContext(logger.Ctx{}), inst: &nicRetirementInstance{Instance: &nicStopCaptureInstance{}, sync: func(string, string, map[string]string) error { t.Fatal("Target memory retirement entered"); return nil }}, name: "eth0", config: desired.Clone(),
				state:       &state.State{ShutdownCtx: context.Background(), BGP: &bgp.Server{}, OVS: func() (*ovs.VSwitch, error) { return nil, nil }},
				volatileGet: func() map[string]string { gets++; return currentVolatile },
				volatileSet: func(map[string]string) error { t.Fatal("Target blind volatile setter entered"); return nil },
			}}
			d.network = &nicStopCaptureNetwork{
				t:       t,
				capture: func(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error { return nil },
				source:  func(context.Context, string, string) (*network.OVNInstanceNICStopOpts, error) { return original, nil },
				retire: func(_ context.Context, id, dev, generation string) (bool, error) {
					clears++
					require.Equal(t, original.InstanceUUID, id)
					require.Equal(t, original.DeviceName, dev)
					require.Equal(t, original.CleanupGeneration, generation)
					return false, nil
				},
				stopOptions: func(opts *network.OVNInstanceNICStopOpts) error {
					stops++
					require.Equal(t, original, opts)
					if fail {
						return networkErr
					}

					return nil
				},
			}

			runConf, err := d.Stop()
			require.NoError(t, err)
			require.Equal(t, 1, stops)
			require.Equal(t, 1, originalCancels)
			require.Zero(t, targetCancels)
			require.Equal(t, desired, d.config)
			// A later volatile read must not select target inputs for the source hook.
			currentVolatile["hwaddr"] = "00:16:3e:07:08:09"
			original.HostVolatile["hwaddr"] = "00:16:3e:0a:0b:0c"
			original.DeviceConfig["network"] = "replacement-after-selection"
			err = runConf.PostHooks[0]()
			if fail {
				require.ErrorIs(t, err, networkErr)
				require.Zero(t, clears)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, clears)
			}

			require.Equal(t, 1, gets)
			require.Equal(t, desired, d.config)
		})
	}
}

func TestNICOVNPreWorkloadCaptureOnly(t *testing.T) {
	for _, name := range []string{"success", "nested", "missing-network", "nil-context", "ovs-error", "ovs-missing", "capture-error", "read-error", "missing-source", "wrong-source"} {
		t.Run(name, func(t *testing.T) {
			captureErr, readErr, ovsErr := errors.New("publish"), errors.New("read"), errors.New("ovs")
			volatile := map[string]string{"hwaddr": "00:16:3e:01:02:03", "last_state.created": "true"}
			config := deviceConfig.Device{"network": "source", "nested": "parent", "ipv4.address": "192.0.2.2/24"}
			if name == "nested" {
				config["nested"] = "parent"
				volatile["host_name"] = "original-nested"
			}

			if name == "ovs-error" || name == "ovs-missing" {
				volatile["host_name"] = "original-host"
				delete(config, "nested")
			}

			originalVolatile, originalConfig := maps.Clone(volatile), config.Clone()
			captures, reads, ovsReads := 0, 0, 0
			st := &state.State{ShutdownCtx: context.Background(), OVS: func() (*ovs.VSwitch, error) {
				ovsReads++
				if name == "ovs-error" {
					return nil, ovsErr
				}

				if name != "ovs-missing" {
					t.Fatal("OVS lookup entered without an applicable host")
				}

				return nil, nil
			}}
			if name == "nil-context" {
				st.ShutdownCtx = nil
			}

			d := &nicOVN{deviceCommon: deviceCommon{
				inst: &nicStopCaptureInstance{}, name: "eth0", config: config, state: st,
				volatileGet: func() map[string]string { return volatile },
				volatileSet: func(map[string]string) error { t.Fatal("capture cannot clear source volatile"); return nil },
			}}
			if name != "missing-network" {
				d.network = &nicStopCaptureNetwork{
					t: t,
					capture: func(port ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error {
						captures++
						require.Empty(t, port)
						require.Equal(t, 19, opts.InstanceID)
						require.Equal(t, "2a981b87-9238-46cb-8e8d-ac766142a773", opts.InstanceUUID)
						require.Equal(t, "eth0", opts.DeviceName)
						require.Equal(t, originalVolatile, opts.HostVolatile)
						require.Equal(t, "192.0.2.2", opts.DeviceConfig["ipv4.address"])
						require.Equal(t, originalVolatile["host_name"], opts.DeviceConfig["host_name"])
						if name == "capture-error" {
							return captureErr
						}

						return nil
					},
					source: func(ctx context.Context, id, dev string) (*network.OVNInstanceNICStopOpts, error) {
						reads++
						require.Equal(t, st.ShutdownCtx, ctx)
						if name == "read-error" {
							return nil, readErr
						}

						if reads == 1 {
							return nil, sql.ErrNoRows
						}

						if name == "missing-source" {
							return nil, nil
						}

						original := &network.OVNInstanceNICStopOpts{CleanupGeneration: "b2500167-d799-426f-8b32-c99b2451918a", InstanceUUID: id, InstanceID: 19, DeviceName: dev, DeviceConfig: deviceConfig.Device{"nested": "parent"}}
						if name == "wrong-source" {
							original.DeviceName = "replacement"
						}

						return original, nil
					},
					stop: func() error { t.Fatal("capture cannot stop a port or dispatch cleanup"); return nil },
				}
			}

			err := d.OVNStopCleanupCapture()
			switch name {
			case "success", "nested":
				require.NoError(t, err)
			case "capture-error":
				require.ErrorIs(t, err, captureErr)
			case "read-error":
				require.ErrorIs(t, err, readErr)
			case "ovs-error":
				require.ErrorIs(t, err, ovsErr)
			default:
				require.Error(t, err)
			}

			switch name {
			case "missing-network", "nil-context":
				require.Zero(t, captures)
				require.Zero(t, reads)
			case "ovs-error", "ovs-missing", "read-error":
				require.Zero(t, captures)
				require.Equal(t, 1, reads)
			default:
				require.Equal(t, 1, captures)
				if name == "capture-error" {
					require.Equal(t, 1, reads)
				} else {
					require.Equal(t, 2, reads)
				}
			}

			if name == "ovs-error" || name == "ovs-missing" {
				require.Equal(t, 1, ovsReads)
			} else {
				require.Zero(t, ovsReads)
			}

			require.Equal(t, originalConfig, config)
			require.Equal(t, originalVolatile, volatile)
			require.Nil(t, st.BGP)
		})
	}
}

func TestNICOVNStoppedReplayGenerationBinding(t *testing.T) {
	for _, name := range []string{"matching", "replacement", "missing-selected"} {
		t.Run(name, func(t *testing.T) {
			selected := "b2500167-d799-426f-8b32-c99b2451918a"
			publicationErr := sql.ErrNoRows
			captures, reads := 0, 0
			d := &nicOVN{deviceCommon: deviceCommon{name: "eth0", state: &state.State{ShutdownCtx: context.Background()}}}
			d.network = &nicStopCaptureNetwork{
				t: t,
				capture: func(_ ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error {
					captures++
					require.Equal(t, selected, opts.CleanupGeneration)
					if name == "missing-selected" {
						return publicationErr
					}

					return nil
				},
				source: func(_ context.Context, id, dev string) (*network.OVNInstanceNICStopOpts, error) {
					reads++
					if name == "missing-selected" {
						return nil, publicationErr
					}

					generation := selected
					if name == "replacement" {
						generation = "e8106174-5bcb-49c7-b2b8-fb6b8807c4a3"
					}

					return &network.OVNInstanceNICStopOpts{CleanupGeneration: generation, InstanceID: 19, InstanceUUID: id, DeviceName: dev, DeviceConfig: deviceConfig.Device{"nested": "parent"}}, nil
				},
				stop: func() error { t.Fatal("source selection cannot execute cleanup"); return nil },
			}

			d.OVNStopCleanupSelectGeneration(selected)
			opts := &network.OVNInstanceNICStopOpts{InstanceUUID: "2a981b87-9238-46cb-8e8d-ac766142a773", DeviceName: "eth0"}
			source, err := d.captureStopSource("", opts)
			require.Zero(t, captures)
			if name == "matching" {
				require.NoError(t, err)
				require.Equal(t, selected, source.CleanupGeneration)
			} else {
				require.Error(t, err)
				require.Nil(t, source)
			}

			require.Equal(t, 1, reads)
		})
	}
}

type nicRetirementNetwork struct {
	ovnNet
	retire func(context.Context, string, string, string) (bool, error)
}

func (n *nicRetirementNetwork) InstanceDevicePortStopRetire(ctx context.Context, id, dev, generation string) (bool, error) {
	return n.retire(ctx, id, dev, generation)
}

type nicRetirementInstance struct {
	instance.Instance
	sync func(string, string, map[string]string) error
}

func (i *nicRetirementInstance) OVNStopCleanupVolatileRetired(id, dev string, original map[string]string) error {
	return i.sync(id, dev, original)
}

func TestNICOVNStopAtomicVolatileRetirementHook(t *testing.T) {
	for _, name := range []string{"matching", "moved", "sql-error", "memory-error", "nil-network", "nil-source", "nil-context", "missing-generation", "unsupported-driver", "wrong-instance-id"} {
		t.Run(name, func(t *testing.T) {
			sqlErr, memErr := errors.New("SQL retirement"), errors.New("memory synchronization")
			source := &network.OVNInstanceNICStopOpts{CleanupGeneration: "generation", InstanceUUID: "original-uuid", InstanceID: 19, DeviceName: "eth0", HostVolatile: map[string]string{"host_name": "original-host"}}
			var events []string
			st := &state.State{ShutdownCtx: context.Background()}
			inst := &nicRetirementInstance{Instance: &nicStopCaptureInstance{}, sync: func(id, dev string, original map[string]string) error {
				events = append(events, "memory")
				require.Equal(t, source.InstanceUUID, id)
				require.Equal(t, source.DeviceName, dev)
				require.Equal(t, source.HostVolatile, original)
				original["host_name"] = "mutated-clone"
				if name == "memory-error" {
					return memErr
				}

				return nil
			}}
			d := &nicOVN{deviceCommon: deviceCommon{state: st, inst: inst}, network: &nicRetirementNetwork{retire: func(ctx context.Context, id, dev, generation string) (bool, error) {
				events = append(events, "SQL")
				require.Equal(t, st.ShutdownCtx, ctx)
				require.Equal(t, source.InstanceUUID, id)
				require.Equal(t, source.DeviceName, dev)
				require.Equal(t, source.CleanupGeneration, generation)
				if name == "sql-error" {
					return false, sqlErr
				}

				return name != "moved", nil
			}}}
			switch name {
			case "nil-network":
				d.network = nil
			case "nil-source":
				source = nil
			case "nil-context":
				st.ShutdownCtx = nil
			case "missing-generation":
				source.CleanupGeneration = ""
			case "unsupported-driver":
				d.inst = &nicStopCaptureInstance{}
			case "wrong-instance-id":
				source.InstanceID = 42
			}

			err := d.retireStopVolatile(source)
			switch name {
			case "matching":
				require.NoError(t, err)
				require.Equal(t, []string{"SQL", "memory"}, events)
			case "moved":
				require.NoError(t, err)
				require.Equal(t, []string{"SQL"}, events)
			case "sql-error":
				require.ErrorIs(t, err, sqlErr)
				require.Equal(t, []string{"SQL"}, events)
			case "memory-error":
				require.ErrorIs(t, err, memErr)
				require.Equal(t, []string{"SQL", "memory"}, events)
			case "wrong-instance-id":
				require.Error(t, err)
				require.Equal(t, []string{"SQL"}, events)
			default:
				require.Error(t, err)
				require.Empty(t, events)
			}

			if source != nil {
				require.Equal(t, "original-host", source.HostVolatile["host_name"])
			}
		})
	}
}

type nicStartAdmissionInstance struct {
	instance.Instance
	config map[string]string
}

func (i *nicStartAdmissionInstance) LocalConfig() map[string]string { return i.config }
func (i *nicStartAdmissionInstance) ID() int                        { return 19 }

func nicStartAdmissionFixture(t *testing.T, name string) *nicOVN {
	t.Helper()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	id := "2a981b87-9238-46cb-8e8d-ac766142a773"
	ctx := context.Background()
	if name == "canceled-context" {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		ctx = canceled
	}

	d := &nicOVN{deviceCommon: deviceCommon{
		inst:   &nicStartAdmissionInstance{config: map[string]string{"volatile.uuid": id}},
		name:   "eth0",
		config: deviceConfig.Device{"network": "current-network"},
		state: &state.State{
			DB: &db.DB{Cluster: cluster}, ShutdownCtx: ctx,
			OVS: func() (*ovs.VSwitch, error) { t.Fatal("OVS entered after rejected NIC start"); return nil, nil },
		},
		volatileGet: func() map[string]string { t.Fatal("Volatile read entered after rejected NIC start"); return nil },
		volatileSet: func(map[string]string) error { t.Fatal("Volatile write entered after rejected NIC start"); return nil },
	}}
	if name == "missing-uuid" {
		d.inst.LocalConfig()["volatile.uuid"] = ""
	}

	if name == "invalid-uuid" {
		d.inst.LocalConfig()["volatile.uuid"] = "invalid"
	}

	if name == "nil-context" {
		d.state.ShutdownCtx = nil
	}

	if name == "nil-db" {
		d.state.DB = nil
	}

	if name == "nil-state" {
		d.state = nil
	}

	require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		if name == "query-failure" {
			_, err := tx.Tx().ExecContext(ctx, "DROP TABLE networks_ovn_nic_cleanup")
			return err
		}

		if name == "no-debt" || name == "continuation-error" || name == "canceled-context" || name == "missing-uuid" || name == "invalid-uuid" || name == "nil-context" || name == "nil-db" || name == "nil-state" {
			return nil
		}

		sourceNodeID := tx.GetNodeID()
		if name == "other-source" {
			sourceNodeID++
		}

		instanceUUID := id
		if name == "other-instance" {
			instanceUUID = uuid.NewString()
		}

		deviceName := "eth0"
		if name == "removed-device" {
			deviceName = "removed-nic"
		}

		completed := name == "completed"
		generation := uuid.NewString()
		_, err := tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload,completed) VALUES (?,?,?,?,1,'[1]','opaque-source-debt',?)", generation, sourceNodeID, instanceUUID, deviceName, completed)
		if err != nil {
			return err
		}

		if name == "retired-metadata" {
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup_retirement(generation,cleared_source) VALUES (?,1)", generation)
		}

		return err
	}))
	return d
}

func TestNICOVNStartCleanupAdmission(t *testing.T) {
	for _, name := range []string{"pending", "removed-device", "retired-metadata", "other-source", "other-instance", "completed", "no-debt", "query-failure", "missing-uuid", "invalid-uuid", "nil-context", "nil-db", "nil-state", "canceled-context", "continuation-error"} {
		t.Run(name, func(t *testing.T) {
			d := nicStartAdmissionFixture(t, name)
			entered := 0
			wantConfig := &deviceConfig.RunConfig{}
			sentinel := errors.New("inert allocation continuation failed")
			result, err := d.startWithCleanupAdmission(func() (*deviceConfig.RunConfig, error) {
				entered++
				if name == "continuation-error" {
					return nil, sentinel
				}

				return wantConfig, nil
			})
			allowed := name == "other-source" || name == "other-instance" || name == "completed" || name == "no-debt" || name == "continuation-error"
			if allowed {
				require.Equal(t, 1, entered)
				if name == "continuation-error" {
					require.ErrorIs(t, err, sentinel)
					require.Nil(t, result)
				} else {
					require.NoError(t, err)
					require.Same(t, wantConfig, result)
				}
			} else {
				require.Error(t, err)
				require.Nil(t, result)
				require.Zero(t, entered)
				if name == "canceled-context" {
					require.ErrorIs(t, err, context.Canceled)
					d.state.ShutdownCtx = context.Background()
					_, err = d.startWithCleanupAdmission(func() (*deviceConfig.RunConfig, error) { entered++; return wantConfig, nil })
					require.NoError(t, err)
					require.Equal(t, 1, entered)
				}
			}
		})
	}
}

func TestNICOVNStartPendingCleanupRejectsActualEntry(t *testing.T) {
	for _, name := range []string{"pending", "removed-device", "retired-metadata", "query-failure", "missing-uuid", "invalid-uuid", "nil-context"} {
		t.Run(name, func(t *testing.T) {
			d := nicStartAdmissionFixture(t, name)
			result, err := d.Start()
			require.Error(t, err)
			require.Nil(t, result)
			// No GlobalConfig/type/environment was supplied: any later Start
			// continuation would fail before host work, instead of this guard.
			if name == "pending" || name == "removed-device" || name == "retired-metadata" {
				require.ErrorContains(t, err, "unacknowledged source OVN NIC cleanup")
			}
		})
	}
}

func TestNICOVNStartDetachedKeepsNoEffectExemption(t *testing.T) {
	d := &nicOVN{deviceCommon: deviceCommon{config: deviceConfig.Device{"attached": "false"}}}
	result, err := d.Start()
	require.NoError(t, err)
	require.Nil(t, result)
}

func nicStopOVSPlan() *ovs.NICPortCleanup {
	return &ovs.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), BridgeUUID: uuid.NewString(), BridgeName: "br-original", PortUUID: uuid.NewString(), InterfaceUUID: uuid.NewString(), InterfaceName: "original-host", OVNPortName: "original-port"}
}

func TestNICOVNStopStoredOVSBeforeCurrentLookup(t *testing.T) {
	for _, name := range []string{"selected", "selected-missing", "selected-replaced", "untyped-source"} {
		t.Run(name, func(t *testing.T) {
			generation := uuid.NewString()
			ovsErr := errors.New("inert original OVS unavailable")
			reads, ovsReads, stops := 0, 0, 0
			original := &network.OVNInstanceNICStopOpts{CleanupGeneration: generation, InstanceID: 19, InstanceUUID: "2a981b87-9238-46cb-8e8d-ac766142a773", DeviceName: "eth0", DeviceConfig: deviceConfig.Device{"network": "original-network"}, OVS: nicStopOVSPlan()}
			if name == "selected-replaced" {
				original.CleanupGeneration = uuid.NewString()
			}

			if name == "untyped-source" {
				original.OVS = nil
			}

			d := &nicOVN{deviceCommon: deviceCommon{
				logger: logger.AddContext(logger.Ctx{}), inst: &nicStopCaptureInstance{}, name: "eth0",
				config:      deviceConfig.Device{"network": "current-network", "host_name": "current-host"},
				state:       &state.State{ShutdownCtx: context.Background(), BGP: &bgp.Server{}, OVS: func() (*ovs.VSwitch, error) { ovsReads++; return nil, ovsErr }},
				volatileGet: func() map[string]string { return nil },
				volatileSet: func(map[string]string) error { t.Fatal("blind volatile clearing entered"); return nil },
			}}
			d.OVNStopCleanupSelectGeneration(generation)
			d.network = &nicStopCaptureNetwork{
				t: t,
				capture: func(ovn.OVNSwitchPort, *network.OVNInstanceNICStopOpts) error {
					t.Fatal("stored source was recaptured")
					return nil
				},
				source: func(context.Context, string, string) (*network.OVNInstanceNICStopOpts, error) {
					reads++
					if name == "selected-missing" {
						return nil, sql.ErrNoRows
					}

					return original, nil
				},
				stopOptions: func(opts *network.OVNInstanceNICStopOpts) error {
					stops++
					require.Equal(t, original.OVS, opts.OVS)
					return nil
				},
				retire: func(context.Context, string, string, string) (bool, error) {
					t.Fatal("retirement after failed OVS acknowledgment")
					return false, nil
				},
			}

			run, err := d.Stop()
			require.Equal(t, 1, reads)
			if name != "selected" {
				require.Error(t, err)
				require.Nil(t, run)
				require.Zero(t, ovsReads)
				require.Zero(t, stops)
				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, ovsReads)
			require.Equal(t, 1, stops)
			// The stored no-host fixture makes postStop inert; only the fake OVS error is reported.
			require.ErrorIs(t, run.PostHooks[0](), ovsErr)
		})
	}
}

func (n *nicStopCaptureNetwork) InstanceDevicePortStopComplete(ctx context.Context, id, dev, generation string) error {
	return nil
}

type nicOriginalReplayInstance struct{ nicStopCaptureInstance }

func (*nicOriginalReplayInstance) Project() api.Project {
	return api.Project{Name: "changed-current-project"}
}

func (*nicOriginalReplayInstance) Name() string            { return "original-instance" }
func (*nicOriginalReplayInstance) Type() instancetype.Type { return instancetype.Container }

func (n *nicStopCaptureNetwork) ID() int64 { return 41 }

func TestNICOVNOriginalSourceLoader(t *testing.T) {
	for _, name := range []string{"success", "network-replaced", "generation-replaced", "volatile-replaced", "source-instance-changed"} {
		t.Run(name, func(t *testing.T) {
			inst := &nicOriginalReplayInstance{}
			source := &network.OVNInstanceNICStopOpts{NetworkID: 41, CleanupGeneration: uuid.NewString(), InstanceUUID: inst.LocalConfig()["volatile.uuid"], InstanceID: 19, DeviceName: "eth0", DeviceConfig: deviceConfig.Device{"type": "nic", "network": "removed-original-name"}, HostVolatile: map[string]string{"host_name": "original"}}
			n := &nicStopCaptureNetwork{t: t, source: func(context.Context, string, string) (*network.OVNInstanceNICStopOpts, error) {
				original := *source
				original.DeviceConfig = source.DeviceConfig.Clone()
				original.HostVolatile = maps.Clone(source.HostVolatile)
				if name == "generation-replaced" {
					original.CleanupGeneration = uuid.NewString()
				}

				if name == "volatile-replaced" {
					original.HostVolatile["host_name"] = "replacement"
				}

				return &original, nil
			}}
			if name == "network-replaced" {
				source.NetworkID = 42
			}

			if name == "source-instance-changed" {
				source.InstanceID = 20
			}

			dev, err := NewOVNStopCleanup(inst, &state.State{ShutdownCtx: context.Background(), OVN: func() (*ovn.NB, *ovn.SB, error) { return nil, nil, nil }}, n, source)
			if name != "success" {
				require.Error(t, err)
				require.Nil(t, dev)
				return
			}

			require.NoError(t, err)
			require.Equal(t, "removed-original-name", dev.Config()["network"])
			require.Equal(t, source.CleanupGeneration, dev.(*nicOVN).stopCleanupGeneration)
			require.Equal(t, "original", dev.(*nicOVN).volatileGet()["host_name"])
			source.HostVolatile["host_name"] = "caller-replacement"
			require.Equal(t, "original", dev.(*nicOVN).volatileGet()["host_name"])
		})
	}
}

type nicOriginalFenceNetwork struct {
	nicStopCaptureNetwork
	fenceErr error
	releases int
}

func (n *nicOriginalFenceNetwork) InstanceDevicePortStopFence(context.Context, string, string, string) (func() error, error) {
	if n.fenceErr != nil {
		return nil, n.fenceErr
	}

	return func() error { n.releases++; return nil }, nil
}

func TestNICOVNOriginalStopFenceBeforeLocalEffects(t *testing.T) {
	for _, mode := range []string{"root-refusal", "physical-authority-refusal"} {
		t.Run(mode, func(t *testing.T) {
			inst := &nicOriginalReplayInstance{}
			generation := uuid.NewString()
			config := deviceConfig.Device{"type": "nic", "network": "original", "nested": "parent"}
			volatile := map[string]string{"host_name": "original"}
			if mode == "physical-authority-refusal" {
				delete(config, "nested")
				config["acceleration"] = "sriov"
				volatile["last_state.ovn.physical"] = `{"Version":0}`
				volatile["last_state.vf.id"] = "0"
			}

			source := &network.OVNInstanceNICStopOpts{NetworkID: 41, CleanupGeneration: generation, InstanceUUID: inst.LocalConfig()["volatile.uuid"], InstanceID: 19, DeviceName: "eth0", DeviceConfig: config.Clone(), HostVolatile: maps.Clone(volatile)}
			if mode == "physical-authority-refusal" {
				source.OVS = &ovs.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), BridgeUUID: uuid.NewString(), BridgeName: "original-bridge", PortUUID: uuid.NewString(), InterfaceUUID: uuid.NewString(), InterfaceName: "original", OVNPortName: "original-port"}
			}

			n := &nicOriginalFenceNetwork{nicStopCaptureNetwork: nicStopCaptureNetwork{t: t, source: func(context.Context, string, string) (*network.OVNInstanceNICStopOpts, error) { return source, nil }, stopOptions: func(*network.OVNInstanceNICStopOpts) error {
				t.Fatal("network effect after authority refusal")
				return nil
			}}}
			if mode == "root-refusal" {
				n.fenceErr = errors.New("original root replaced")
			}

			ovsEffects := 0
			d := &nicOVN{network: n, stopCleanupGeneration: generation, deviceCommon: deviceCommon{inst: inst, name: "eth0", config: config, state: &state.State{ShutdownCtx: context.Background(), DB: &db.DB{Cluster: nil}, OVS: func() (*ovs.VSwitch, error) { ovsEffects++; return nil, errors.New("unexpected OVS effect") }}, logger: logger.AddContext(logger.Ctx{}), volatileGet: func() map[string]string { return maps.Clone(volatile) }}}
			if mode == "physical-authority-refusal" {
				cluster, cleanup := db.NewTestCluster(t)
				defer cleanup()
				d.state.DB.Cluster = cluster
			}

			run, err := d.Stop()
			require.Error(t, err)
			require.Nil(t, run)
			require.Zero(t, ovsEffects)
			if mode == "physical-authority-refusal" {
				require.Equal(t, 1, n.releases)
			}
		})
	}
}
