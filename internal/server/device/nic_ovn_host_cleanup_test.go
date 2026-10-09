package device

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	pcidev "github.com/lxc/incus/v7/internal/server/device/pci"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/state"
)

func TestNICOVNPhysicalOriginalCleanup(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	for _, name := range []string{"success", "source-changed", "pci-replacement", "representor-replacement", "vdpa-replacement", "delete-refused", "delete-reply-lost", "restore-refused", "restore-unacknowledged", "rollback-prebackend", "rollback-backend-attempted", "rollback-restore-refused", "rollback-release-refused", "rollback-retire-refused", "earlier-boot", "rollback-earlier-boot"} {
		t.Run(name, func(t *testing.T) {
			inst := &nicOriginalReplayInstance{}
			d := &nicOVN{network: &nicStopCaptureNetwork{}}
			d.deviceCommon = deviceCommon{inst: inst, name: "eth0", config: deviceConfig.Device{"acceleration": "vdpa"}, state: &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background()}}
			p := nicOVNPhysicalCleanup{Version: 1, NetworkID: 41, SourceNodeID: cluster.GetNodeID(), InstanceID: 19, InstanceUUID: inst.LocalConfig()["volatile.uuid"], DeviceName: "eth0", Mode: "vdpa", Parent: "pf0", VFID: 7, PFPath: "/original/pf", PFInode: 11, VFPath: "/original/vf", VFInode: 22, VDPAName: "original-vdpa", VDPAInode: 33, Representor: ip.NICLinkCleanup{Name: "original-representor", Alias: "original-generation"}}
			if name == "source-changed" {
				p.SourceNodeID++
			}

			if strings.HasSuffix(name, "earlier-boot") {
				// A host crash and reboot discarded the VF settings, marker and vDPA device.
				p.Representor.BootID = "00000000-0000-4000-8000-000000000001"
			}

			raw, err := json.Marshal(p)
			require.NoError(t, err)
			v := map[string]string{"host_name": "original-vf", "last_state.vf.parent": "pf0", "last_state.vf.id": "7", "last_state.vdpa.name": p.VDPAName, "last_state.ovn.physical": string(raw)}
			before := maps.Clone(v)
			var events []string
			var gone bool
			failure := errors.New("injected effect refusal")
			held := func() {
				if network.SRIOVVirtualFunctionMutex.TryLock() {
					network.SRIOVVirtualFunctionMutex.Unlock()
					t.Fatal("allocation mutex must span the original physical effects")
				}
			}

			ops := nicOVNPhysicalOps{
				paths: func(parent string, id int) (string, uint64, string, uint64, error) {
					require.Equal(t, "pf0", parent)
					require.Equal(t, 7, id)
					if name == "pci-replacement" {
						return p.PFPath, 99, p.VFPath, p.VFInode, nil
					}

					return p.PFPath, p.PFInode, p.VFPath, p.VFInode, nil
				},
				verify: func(plan ip.NICLinkCleanup) error {
					held()
					require.Equal(t, p.Representor, plan)
					if name == "representor-replacement" {
						return failure
					}

					return nil
				},
				vdpaInode: func(device string) (uint64, error) {
					require.Equal(t, p.VDPAName, device)
					if gone {
						return 0, os.ErrNotExist
					}

					if name == "vdpa-replacement" {
						return 99, nil
					}

					return p.VDPAInode, nil
				},
				deleteVDPA: func(device string) error {
					held()
					events = append(events, "delete-original-vdpa")
					if name == "delete-refused" {
						return failure
					}

					gone = true
					if name == "delete-reply-lost" {
						return failure
					}

					return nil
				},
				restore: func(original map[string]string) error {
					held()
					require.Equal(t, before, original)
					events = append(events, "restore-original-vf")
					if name == "restore-refused" || name == "rollback-restore-refused" {
						return failure
					}

					return nil
				},
				acknowledgeRestore: func(plan nicOVNPhysicalCleanup, original map[string]string) error {
					held()
					require.Equal(t, p, plan)
					require.Equal(t, before, original)
					if name == "restore-unacknowledged" {
						return failure
					}

					return nil
				},
			}

			if strings.HasPrefix(name, "rollback-") {
				err = d.rollbackPhysicalHostWithOps(v, name == "rollback-backend-attempted", ops, func(plan ip.NICLinkCleanup) error {
					held()
					require.Equal(t, p.Representor, plan)
					events = append(events, "release-original-marker")
					if name == "rollback-release-refused" {
						return failure
					}

					return nil
				}, func(original map[string]string) error {
					held()
					require.Equal(t, before, original)
					events = append(events, "retire-original-metadata")
					if name == "rollback-retire-refused" {
						return failure
					}

					return nil
				})
			} else {
				err = d.postStopPhysicalWithOps(v, ops)
			}

			if name == "success" || name == "delete-reply-lost" || name == "rollback-prebackend" || name == "rollback-backend-attempted" || strings.HasSuffix(name, "earlier-boot") {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			if name == "rollback-earlier-boot" {
				require.Equal(t, []string{"retire-original-metadata"}, events)
			}

			if name == "source-changed" || name == "pci-replacement" || name == "representor-replacement" || name == "vdpa-replacement" || name == "earlier-boot" {
				require.Empty(t, events)
			}

			if name == "delete-refused" {
				require.Equal(t, []string{"delete-original-vdpa"}, events)
			}

			if name == "rollback-prebackend" {
				require.Equal(t, []string{"delete-original-vdpa", "restore-original-vf", "release-original-marker", "retire-original-metadata"}, events)
			}

			if name == "rollback-backend-attempted" || name == "rollback-restore-refused" {
				require.Equal(t, []string{"delete-original-vdpa", "restore-original-vf"}, events)
			}

			if name == "rollback-release-refused" {
				require.Equal(t, []string{"delete-original-vdpa", "restore-original-vf", "release-original-marker"}, events)
			}

			require.Equal(t, before, v, "failure and success leave retirement to the enclosing rooted Stop")
			require.True(t, network.SRIOVVirtualFunctionMutex.TryLock())
			network.SRIOVVirtualFunctionMutex.Unlock()
		})
	}
}

func TestNICOVNRemoveRetainsPhysicalPreclaim(t *testing.T) {
	d := &nicOVN{deviceCommon: deviceCommon{volatileGet: func() map[string]string {
		return map[string]string{"last_state.ovn.physical": "captured-before-PCI-effect"}
	}}}
	err := d.Remove(true)
	require.ErrorContains(t, err, "quarantined")
}

func TestNICOVNStopVirtualClaimSourceBeforeLookup(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	for _, changed := range []string{"network", "member", "uuid", "instance", "device", "incomplete"} {
		t.Run(changed, func(t *testing.T) {
			inst := &nicOriginalReplayInstance{}
			d := &nicOVN{network: &nicStopCaptureNetwork{t: t}, deviceCommon: deviceCommon{inst: inst, name: "eth0", state: &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background()}}}
			claim := nicOVNVirtualClaim{NetworkID: 41, SourceNodeID: cluster.GetNodeID(), InstanceUUID: inst.LocalConfig()["volatile.uuid"], InstanceID: inst.ID(), DeviceName: "eth0"}
			switch changed {
			case "network":
				claim.NetworkID++
			case "member":
				claim.SourceNodeID++
			case "uuid":
				claim.InstanceUUID = "replacement"
			case "instance":
				claim.InstanceID++
			case "device":
				claim.DeviceName = "replacement"
			case "incomplete":
				claim.SourceNodeID = 0
			}

			raw, err := json.Marshal(claim)
			require.NoError(t, err)
			_, err = d.captureStopSource("", &network.OVNInstanceNICStopOpts{InstanceUUID: inst.LocalConfig()["volatile.uuid"], InstanceID: inst.ID(), DeviceName: "eth0", DeviceConfig: deviceConfig.Device{"host_name": "original"}, HostVolatile: map[string]string{"last_state.ovn.host": string(raw)}})
			require.ErrorContains(t, err, "source allocation changed")
		})
	}
}

func TestNICOVNPhysicalSetupVFBeforeEffect(t *testing.T) {
	for _, mode := range []string{"snapshot-failure", "pci-failure", "claim-failure", "claimed", "ordinary-no-callback"} {
		t.Run(mode, func(t *testing.T) {
			failure := errors.New("injected stage failure")
			var events []string
			original := map[string]string{}
			ops := networkSRIOVSetupVFOps{
				parentInfo: func(parent string, id int) (ip.VirtFuncInfo, error) {
					require.Equal(t, "pf0", parent)
					require.Equal(t, 7, id)
					return ip.VirtFuncInfo{Address: net.HardwareAddr{0, 1, 2, 3, 4, 5}, VLAN: 9, SpoofCheck: true, Trusted: 1}, nil
				},
				snapshot: func(host string, values map[string]string) error {
					if mode == "snapshot-failure" {
						return failure
					}

					require.Equal(t, "original-vf", host)
					values["last_state.hwaddr"], values["last_state.mtu"], values["last_state.pci.driver"] = "original-MAC", "1500", "original-driver"
					return nil
				},
				pciSlot: func(parent, id string) (pcidev.Device, error) {
					if mode == "pci-failure" {
						return pcidev.Device{}, failure
					}

					return pcidev.Device{SlotName: "0000:01:00.7"}, nil
				},
				unbind: func(pci pcidev.Device) error {
					require.Equal(t, "0000:01:00.7", pci.SlotName)
					events = append(events, "unbind")
					return failure // Stop at the first effect; no hardware access.
				},
			}

			var capture []func() error
			if mode != "ordinary-no-callback" {
				capture = []func() error{func() error {
					events = append(events, "capture")
					for key, value := range map[string]string{"host_name": "original-vf", "last_state.created": "false", "last_state.vf.parent": "pf0", "last_state.vf.id": "7", "last_state.vf.hwaddr": "00:01:02:03:04:05", "last_state.vf.vlan": "9", "last_state.vf.spoofcheck": "true", "last_state.vf.trusted": "1", "last_state.hwaddr": "original-MAC", "last_state.mtu": "1500", "last_state.pci.driver": "original-driver"} {
						require.Equal(t, value, original[key], key)
					}

					if mode == "claim-failure" {
						return failure
					}

					original["last_state.ovn.physical"] = "durable-original-claim"
					return nil
				}}
			}

			_, _, err := networkSRIOVSetupVFWithOps(deviceCommon{}, "pf0", "original-vf", 7, original, ops, capture...)
			require.ErrorIs(t, err, failure)
			switch mode {
			case "snapshot-failure", "pci-failure":
				require.Empty(t, events)
			case "claim-failure":
				require.Equal(t, []string{"capture"}, events)
			case "claimed":
				require.Equal(t, []string{"capture", "unbind"}, events)
				require.Equal(t, "durable-original-claim", original["last_state.ovn.physical"])
			case "ordinary-no-callback":
				require.Equal(t, []string{"unbind"}, events)
			}
		})
	}
}
