package network

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// These fixtures initialize only the private CowSQL cluster. State is a literal;
// no firewall, OVS, NB/SB client, driver or workload is constructed or invoked.
func nicStopSourceFixture(t *testing.T) (*ovn, *OVNInstanceNICStopOpts, ovnNICStopSourceSnapshot, map[int64]string) {
	t.Helper()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	n := &ovn{}
	n.state = &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background(), ServerName: "source-member"}
	var id int64
	token := uuid.NewString()
	err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "source-nic-cleanup", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		err = tx.NetworkCreated(api.ProjectDefaultName, "source-nic-cleanup")
		if err != nil {
			return err
		}

		err = tx.NetworkNodeCreated(id)
		if err != nil {
			return err
		}

		return tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "source-nic-cleanup", token, "nic")
	})
	require.NoError(t, err)
	n.id = id
	n.ovnOperationToken = token
	opts := &OVNInstanceNICStopOpts{
		InstanceUUID: uuid.NewString(), InstanceID: 42, DeviceName: "eth0",
		DeviceConfig: deviceConfig.Device{"network": "original-network", "ipv4.address.external": "192.0.2.42"},
		HostVolatile: map[string]string{"host_name": "original-host", "last_state.ip_addresses": "198.51.100.42"},
	}

	opts.OVS = &ovs.NICPortCleanup{Version: 1, RootUUID: uuid.NewString(), BridgeUUID: uuid.NewString(), BridgeName: "br-original", PortUUID: uuid.NewString(), InterfaceUUID: uuid.NewString(), InterfaceName: "original-host", OVNPortName: "original-port"}
	root := uuid.NewString()
	source := ovnNICStopSourceSnapshot{
		Port:   networkOVN.NICPortCleanup{Version: 1, RootUUID: root, SwitchUUID: uuid.NewString(), SwitchName: "original-switch", PortUUID: uuid.NewString(), PortVersion: uuid.NewString(), PortName: "original-port", Source: n.state.ServerName},
		DNSIPs: []net.IP{net.ParseIP("198.51.100.42")}, Prefixes: []net.IPNet{IPToNet(net.ParseIP("198.51.100.42"))},
		NAT:        &networkOVN.NICNATCleanup{Version: 1, RootUUID: root, RouterUUID: uuid.NewString(), RouterName: "original-router", Rows: []networkOVN.NICNATCleanupRow{}},
		RouterPort: "original-router-port", ProxyPort: "original-proxy-port", DNSConfig: map[string]string{"dns.zone.forward": "original-zone"},
	}

	owner := networkOVN.NICPrefixOwner{RootUUID: root, SwitchUUID: source.Port.SwitchUUID, SwitchName: source.Port.SwitchName, PortUUID: source.Port.PortUUID, PortName: source.Port.PortName, PortVersion: uuid.NewString(), Source: n.state.ServerName, Generation: uuid.NewString(), Previous: strings.Repeat("0", 64)}
	source.AddressSets = &networkOVN.NICAddressSetCleanup{Version: 2, RootUUID: root, Rows: []networkOVN.NICAddressSetCleanupRow{
		{UUID: uuid.NewString(), Version: uuid.NewString(), Name: fmt.Sprintf("%s_ip4", acl.OVNIntSwitchPortGroupAddressSetPrefix(id)), Ledger: strings.Repeat("0", 64), Ownership: networkOVN.NICPrefixCleanup{Owner: owner, Prefixes: []string{"198.51.100.42/32"}}},
		{UUID: uuid.NewString(), Version: uuid.NewString(), Name: fmt.Sprintf("%s_ip6", acl.OVNIntSwitchPortGroupAddressSetPrefix(id)), Ledger: strings.Repeat("0", 64), Ownership: networkOVN.NICPrefixCleanup{Owner: owner, Prefixes: []string{}}},
	}}
	source.MACBindings = &networkOVN.NICMACBindingCleanup{Version: 1, RootUUID: uuid.NewString(), NBRootUUID: root, NBRouterUUID: source.NAT.RouterUUID, DatapathUUID: uuid.NewString(), DatapathVersion: uuid.NewString(), Port: source.RouterPort, Rows: []networkOVN.NICMACBindingCleanupRow{}}
	return n, opts, source, map[int64]string{id: token}
}

func TestNICStopSourcePublicationBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		persistFail, staleToken, lateFail, cancelAfter bool
		want                                           []string
	}{
		{name: "success", want: []string{"publish", "before", "after"}},
		{name: "persistence-failure", persistFail: true, want: []string{"publish"}},
		{name: "stale-capability", staleToken: true, want: []string{"publish"}},
		{name: "later-effect-failure-retains-original-debt", lateFail: true, want: []string{"publish", "before", "after"}},
		{name: "cancellation-after-publication-retains-original-debt", cancelAfter: true, want: []string{"publish"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			if tc.persistFail {
				require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, `CREATE TRIGGER reject_nic_source_capture BEFORE INSERT ON networks_ovn_nic_cleanup BEGIN SELECT RAISE(ABORT, 'injected source capture failure'); END`)
					return err
				}))
			}

			if tc.staleToken {
				tokens[n.ID()] = "stale"
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []string
			err := stopNICCapturedRoutes(ctx, nil, nil, func(plans []networkOVN.NICRouteCleanup) error {
				events = append(events, "publish")
				source.Routes = plans
				_, err := n.publishNICStopSource(ctx, opts, source, tokens)
				if tc.cancelAfter {
					cancel()
				}

				return err
			}, func() error { events = append(events, "before"); return nil }, func() error {
				events = append(events, "after")
				if tc.lateFail {
					return errors.New("late effect failed")
				}

				return nil
			})
			if tc.persistFail || tc.staleToken || tc.lateFail || tc.cancelAfter {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.want, events)
			stored, a, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
			require.NoError(t, err)
			if tc.persistFail || tc.staleToken {
				require.Nil(t, stored)
				require.Nil(t, a)
			} else {
				require.NotNil(t, stored)
				require.NotNil(t, a)
				require.Equal(t, "original-port", string(stored.Port.PortName))
				require.False(t, a.Completed)
				// A successful network-only continuation still does not acknowledge BGP/host hooks.
				require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, n.ID()))
					return nil
				}))
			}
		})
	}
}

func TestNICStopSourcePendingKeepsOriginalInputs(t *testing.T) {
	n, opts, source, tokens := nicStopSourceFixture(t)
	originalUUID := opts.InstanceUUID
	a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.NoError(t, err)
	// Caller mutations and new Desired inputs cannot replace the already durable source payload.
	opts.DeviceConfig["network"] = "target-network"
	opts.HostVolatile["host_name"] = "target-host"
	source.DNSIPs[0][0] ^= 1
	source.DNSConfig["dns.zone.forward"] = "target-zone"
	stored, again, err := n.pendingNICStopSource(context.Background(), originalUUID, "eth0")
	require.NoError(t, err)
	require.Equal(t, a, *again)
	require.Equal(t, "original-network", stored.DeviceConfig["network"])
	require.Equal(t, "original-host", stored.HostVolatile["host_name"])
	require.Equal(t, "198.51.100.42", stored.DNSIPs[0].String())
	require.Equal(t, "original-zone", stored.DNSConfig["dns.zone.forward"])
	require.Equal(t, networkOVN.OVNRouterPort("original-router-port"), stored.RouterPort)
	require.Equal(t, 42, stored.InstanceID)
	replacement, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.Error(t, err)
	require.Empty(t, replacement.Generation)
	stored, again, err = n.pendingNICStopSource(context.Background(), originalUUID, "eth0")
	require.NoError(t, err)
	require.Equal(t, a, *again)
	require.Equal(t, "original-network", stored.DeviceConfig["network"])
	missing, missingAttempt, err := n.pendingNICStopSource(context.Background(), uuid.NewString(), "eth0")
	require.NoError(t, err)
	require.Nil(t, missing)
	require.Nil(t, missingAttempt)
}

func TestNICStopSourcePendingRefusals(t *testing.T) {
	for _, tc := range []string{"source-name", "network-id", "future-payload", "malformed-payload", "canceled-read"} {
		t.Run(tc, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			require.NoError(t, err)
			ctx := context.Background()
			switch tc {
			case "source-name":
				n.state.ServerName = "other-member"
			case "network-id":
				n.id++
			case "future-payload", "malformed-payload":
				payload := `{"Kind":"incus-ovn-nic-stop","Version":2}`
				if tc == "malformed-payload" {
					payload = "{"
				}

				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET payload=? WHERE generation=?", payload, a.Generation)
					return err
				}))
			case "canceled-read":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			stored, attempt, err := n.pendingNICStopSource(ctx, opts.InstanceUUID, opts.DeviceName)
			require.Error(t, err)
			require.Nil(t, stored)
			require.Nil(t, attempt)
			if tc == "canceled-read" {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestNICStopSourceDeviceLookupIsSourceScoped(t *testing.T) {
	n, opts, source, tokens := nicStopSourceFixture(t)
	a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.NoError(t, err)
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		foreignNode, err := tx.CreateNode("other-original-source", "192.0.2.55:8443")
		require.NoError(t, err)
		foreignGeneration := uuid.NewString()
		networkIDsJSON := fmt.Sprintf("[%d]", n.ID())
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup (generation,source_node_id,instance_uuid,device_name,version,network_ids,payload) VALUES (?,?,?,?,?,?,?)",
			foreignGeneration, foreignNode, opts.InstanceUUID, opts.DeviceName, 1, networkIDsJSON, a.Payload)
		require.NoError(t, err)
		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup_networks (generation,source_node_id,network_id) VALUES (?,?,?)", foreignGeneration, foreignNode, n.ID())
		require.NoError(t, err)

		got, err := tx.OVNNICCleanupForDevice(ctx, opts.InstanceUUID, opts.DeviceName)
		require.NoError(t, err)
		require.Equal(t, a, got)
		_, err = tx.OVNNICCleanupForDevice(ctx, opts.InstanceUUID, "other-device")
		require.ErrorIs(t, err, sql.ErrNoRows)
		// Fixture-only completion proves that foreign source debt is not selected as local.
		// It is not an acknowledgment of any actual backend/host cleanup.
		require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
		_, err = tx.OVNNICCleanupForDevice(ctx, opts.InstanceUUID, opts.DeviceName)
		require.ErrorIs(t, err, sql.ErrNoRows)
		foreign, err := tx.OVNNICCleanupByGeneration(ctx, foreignGeneration)
		require.NoError(t, err)
		require.Equal(t, foreignNode, foreign.SourceNodeID)
		require.False(t, foreign.Completed)
		return nil
	}))
}

func TestNICStopSourcePublicationRejectsInvalidPortIdentity(t *testing.T) {
	for _, tc := range []string{"foreign-source", "malformed-root", "missing-switch", "missing-port", "missing-row-version", "missing-port-name", "nil-options"} {
		t.Run(tc, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			actor := opts.InstanceUUID
			switch tc {
			case "foreign-source":
				source.Port.Source = "other-member"
			case "malformed-root":
				source.Port.RootUUID = "unsupported-root"
			case "missing-switch":
				source.Port.SwitchUUID = ""
			case "missing-port":
				source.Port.PortUUID = ""
			case "missing-row-version":
				source.Port.PortVersion = ""
			case "missing-port-name":
				source.Port.PortName = ""
			case "nil-options":
				opts = nil
			}

			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			require.Error(t, err)
			require.Empty(t, a.Generation)
			stored, attempt, err := n.pendingNICStopSource(context.Background(), actor, "eth0")
			require.NoError(t, err)
			require.Nil(t, stored)
			require.Nil(t, attempt)
		})
	}
}

func TestNICStopNATOriginalAddressTuples(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  deviceConfig.Device
		ips     []net.IP
		want    []networkOVN.NICNATCleanupTuple
		failure bool
	}{
		{name: "no-external", config: deviceConfig.Device{}},
		{name: "ipv4", config: deviceConfig.Device{"ipv4.address.external": "198.51.100.10"}, ips: []net.IP{net.ParseIP("192.0.2.10")}, want: []networkOVN.NICNATCleanupTuple{
			{Type: "snat", LogicalIP: "192.0.2.10/32", ExternalIP: "198.51.100.10"},
			{Type: "dnat_and_snat", LogicalIP: "192.0.2.10", ExternalIP: "198.51.100.10"},
		}},
		{name: "ipv6", config: deviceConfig.Device{"ipv6.address.external": "2001:db8:2::10"}, ips: []net.IP{net.ParseIP("2001:db8:1::10")}, want: []networkOVN.NICNATCleanupTuple{
			{Type: "snat", LogicalIP: "2001:db8:1::10/128", ExternalIP: "2001:db8:2::10"},
			{Type: "dnat_and_snat", LogicalIP: "2001:db8:1::10", ExternalIP: "2001:db8:2::10"},
		}},
		{name: "missing-original-internal", config: deviceConfig.Device{"ipv4.address.external": "198.51.100.10"}, failure: true},
		{name: "invalid-external", config: deviceConfig.Device{"ipv4.address.external": "bad"}, failure: true},
		{name: "wrong-external-family", config: deviceConfig.Device{"ipv4.address.external": "2001:db8::10"}, failure: true},
		{name: "wrong-internal-family", config: deviceConfig.Device{"ipv4.address.external": "198.51.100.10"}, ips: []net.IP{net.ParseIP("2001:db8::10")}, failure: true},
		{name: "ambiguous-internal", config: deviceConfig.Device{"ipv4.address.external": "198.51.100.10"}, ips: []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("192.0.2.20")}, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tuples, err := nicStopNATTuples(tc.config, tc.ips)
			if tc.failure {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, tuples)
		})
	}
}

func TestNICStopNATSourceCaptureRequiredBeforeEffects(t *testing.T) {
	for _, name := range []string{"missing-capture", "foreign-root", "invalid-router", "foreign-tuple", "invalid-row", "captured-absence"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			switch name {
			case "missing-capture":
				source.NAT = nil
			case "foreign-root":
				source.NAT.RootUUID = uuid.NewString()
			case "invalid-router":
				source.NAT.RouterUUID = "bad"
			case "foreign-tuple":
				source.NAT.Rows = []networkOVN.NICNATCleanupRow{{UUID: uuid.NewString(), Version: uuid.NewString(), Type: "snat", LogicalIP: "198.51.100.99/32", ExternalIP: "192.0.2.42"}}
			case "invalid-row":
				source.NAT.Rows = []networkOVN.NICNATCleanupRow{{UUID: "bad", Version: uuid.NewString(), Type: "snat", LogicalIP: "198.51.100.42/32", ExternalIP: "192.0.2.42"}}
			}

			entered := false
			err := stopNICCapturedRoutes(context.Background(), &nicStopRoutesFixture{t: t}, nil,
				func([]networkOVN.NICRouteCleanup) error {
					_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
					return err
				},
				func() error { entered = true; return nil }, func() error { return nil })
			switch name {
			case "captured-absence":
				require.NoError(t, err)
				require.True(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.NotNil(t, stored.NAT)
				require.Equal(t, source.NAT, stored.NAT)
			default:
				require.Error(t, err)
				require.False(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Nil(t, stored)
			}
		})
	}
}

func TestNICStopMACOriginalRouterBinding(t *testing.T) {
	for _, name := range []string{"local", "missing-local", "peer-only", "wrong-root", "duplicate-local", "invalid-uuid"} {
		t.Run(name, func(t *testing.T) {
			root, router := uuid.NewString(), uuid.NewString()
			plans := []networkOVN.NICRouteCleanup{{Version: 1, RootUUID: root, RouterUUID: router, RouterName: "original-local"}}
			switch name {
			case "missing-local":
				plans = nil
			case "peer-only":
				plans[0].RouterName = "peer"
			case "wrong-root":
				plans[0].RootUUID = uuid.NewString()
			case "duplicate-local":
				plans = append(plans, plans[0])
			case "invalid-uuid":
				plans[0].RouterUUID = "bad"
			}

			id, err := nicStopMACRouterUUID(root, "original-local", plans)
			switch name {
			case "local":
				require.NoError(t, err)
				require.Equal(t, router, id)
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestNICStopMACCaptureRequiredBeforeEffects(t *testing.T) {
	for _, name := range []string{"missing-capture", "wrong-nb-root", "foreign-nb-router", "wrong-port", "invalid-datapath", "foreign-ip", "captured-absence"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			switch name {
			case "missing-capture":
				source.MACBindings = nil
			case "wrong-nb-root":
				source.MACBindings.NBRootUUID = uuid.NewString()
			case "foreign-nb-router":
				source.MACBindings.NBRouterUUID = uuid.NewString()
			case "wrong-port":
				source.MACBindings.Port = "foreign-router-port"
			case "invalid-datapath":
				source.MACBindings.DatapathUUID = "bad"
			case "foreign-ip":
				source.MACBindings.Rows = []networkOVN.NICMACBindingCleanupRow{{UUID: uuid.NewString(), Version: uuid.NewString(), IP: "198.51.100.99"}}
			}

			entered := false
			err := stopNICCapturedRoutes(context.Background(), &nicStopRoutesFixture{t: t}, nil,
				func([]networkOVN.NICRouteCleanup) error {
					_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
					return err
				},
				func() error { entered = true; return nil }, func() error { return nil })
			switch name {
			case "captured-absence":
				require.NoError(t, err)
				require.True(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.NotNil(t, stored.MACBindings)
				require.Equal(t, source.MACBindings, stored.MACBindings)
			default:
				require.Error(t, err)
				require.False(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Nil(t, stored)
			}
		})
	}
}

func TestNICStopSourcePublicViewKeepsOriginal(t *testing.T) {
	n, opts, source, tokens := nicStopSourceFixture(t)
	a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.NoError(t, err)
	opts.InstanceID = 97
	opts.DeviceConfig["network"] = "target-network"
	opts.HostVolatile["host_name"] = "target-host"
	view, err := n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
	require.NoError(t, err)
	require.Equal(t, a.Generation, view.CleanupGeneration)
	require.Equal(t, a.InstanceUUID, view.InstanceUUID)
	require.Equal(t, a.DeviceName, view.DeviceName)
	require.Equal(t, 42, view.InstanceID)
	require.Equal(t, "original-network", view.DeviceConfig["network"])
	require.Equal(t, "original-host", view.HostVolatile["host_name"])
	view.DeviceConfig["network"] = "mutated-view"
	view.HostVolatile["host_name"] = "mutated-view"
	again, err := n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
	require.NoError(t, err)
	require.Equal(t, "original-network", again.DeviceConfig["network"])
	require.Equal(t, "original-host", again.HostVolatile["host_name"])
}

func TestNICStopSourcePublicViewRefusals(t *testing.T) {
	for _, tc := range []string{"nil-context", "canceled", "missing", "source-name", "network-id", "future-payload"} {
		t.Run(tc, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			require.NoError(t, err)
			ctx := context.Background()
			id := opts.InstanceUUID
			switch tc {
			case "nil-context":
				ctx = nil //nolint:staticcheck // Intentional rejected input.
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing":
				id = uuid.NewString()
			case "source-name":
				n.state.ServerName = "target-member"
			case "network-id":
				n.id++
			case "future-payload":
				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_cleanup SET payload=? WHERE generation=?", `{"Kind":"incus-ovn-nic-stop","Version":2}`, a.Generation)
					return err
				}))
			}

			view, err := n.InstanceDevicePortStopSource(ctx, id, opts.DeviceName)
			require.Error(t, err)
			require.Nil(t, view)
			if tc == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
				view, err = n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Equal(t, a.Generation, view.CleanupGeneration)
			}
		})
	}
}

func TestNICStopSourceGenerationRefusesReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, expected, actual string
		missing, wantError     bool
	}{
		{name: "fresh"},
		{name: "same-original", expected: "original", actual: "original"},
		{name: "new-generation", expected: "original", actual: "replacement", wantError: true},
		{name: "missing-original", expected: "original", missing: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a *db.OVNNICCleanup
			if !tc.missing {
				a = &db.OVNNICCleanup{Generation: tc.actual}
			}

			err := nicStopCheckGeneration(tc.expected, a)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNICStopSourceGenerationGuardsActualStop(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "replacement"
		if missing {
			name = "missing"
		}

		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			n.project, n.name = api.ProjectDefaultName, "source-nic-cleanup"
			if !missing {
				_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
				require.NoError(t, err)
			}

			opts.CleanupGeneration = uuid.NewString()
			// Actual public Stop under its already held database reservation.
			// There is no NB/SB client; the strict generation refusal precedes it.
			err := n.InstanceDevicePortStop("", opts)
			require.ErrorContains(t, err, "generation changed or is missing")
			require.Nil(t, n.ovnnb)
			require.Nil(t, n.ovnsb)
		})
	}
}

func TestNICStopSourceReplayDecoder(t *testing.T) {
	for _, name := range []string{"valid", "completed", "wrong-member", "wrong-source-name", "unsupported", "bad-generation", "bad-instance", "missing-name", "malformed", "missing-type", "missing-network", "wrong-primary-network", "bad-port"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			opts.DeviceConfig["type"] = "nic"
			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(a.Payload), &payload))
			nodeID, sourceName := n.state.DB.Cluster.GetNodeID(), n.state.ServerName
			switch name {
			case "completed":
				a.Completed = true
			case "wrong-member":
				nodeID++
			case "wrong-source-name":
				sourceName = "replacement-member"
			case "unsupported":
				a.Version++
			case "bad-generation":
				a.Generation = "bad"
			case "bad-instance":
				a.InstanceUUID = "bad"
			case "missing-name":
				a.DeviceName = ""
			case "malformed":
				a.Payload = "{"
			case "missing-type":
				delete(payload["DeviceConfig"].(map[string]any), "type")
			case "missing-network":
				delete(payload["DeviceConfig"].(map[string]any), "network")
			case "wrong-primary-network":
				payload["NetworkID"] = float64(n.ID() + 10)
			case "bad-port":
				payload["Port"].(map[string]any)["PortUUID"] = "bad"
			}

			if name != "malformed" {
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				a.Payload = string(raw)
			}

			got, networkID, err := OVNNICCleanupSource(a, nodeID, sourceName)
			if name != "valid" {
				require.Error(t, err)
				require.Nil(t, got)
				require.Zero(t, networkID)
				return
			}

			require.NoError(t, err)
			require.Equal(t, n.ID(), networkID)
			require.Equal(t, opts.InstanceID, got.InstanceID)
			require.Equal(t, a.Generation, got.CleanupGeneration)
			require.Equal(t, opts.DeviceConfig, got.DeviceConfig)
			require.Equal(t, opts.HostVolatile, got.HostVolatile)
			got.DeviceConfig["network"] = "replacement"
			got.HostVolatile["host_name"] = "replacement"
			again, _, err := OVNNICCleanupSource(a, nodeID, sourceName)
			require.NoError(t, err)
			require.Equal(t, "original-network", again.DeviceConfig["network"])
			require.Equal(t, "original-host", again.HostVolatile["host_name"])
		})
	}
}

func TestNICStopSourceVolatileRetirement(t *testing.T) {
	for _, name := range []string{"matching", "moved", "wrong-generation", "missing-source", "nil-context", "missing-generation", "receipt-failure"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			require.NoError(t, err)
			require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(id,node_id,name,architecture,type,description,project_id) VALUES (?,?, 'retire-source',1,0,'',(SELECT id FROM projects WHERE name='default'))", opts.InstanceID, tx.GetNodeID())
				if err != nil {
					return err
				}

				for key, value := range map[string]string{"volatile.uuid": opts.InstanceUUID, "volatile.eth0.host_name": opts.HostVolatile["host_name"]} {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", opts.InstanceID, key, value)
					if err != nil {
						return err
					}
				}

				if name == "moved" {
					result, err := tx.Tx().ExecContext(ctx, "INSERT INTO nodes(name,address,schema,api_extensions,description,arch) VALUES ('retire-target','192.0.2.98',84,0,'',1)")
					if err != nil {
						return err
					}

					id, err := result.LastInsertId()
					if err != nil {
						return err
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", id, opts.InstanceID)
					return err
				}

				if name == "receipt-failure" {
					_, err = tx.Tx().ExecContext(ctx, "CREATE TRIGGER fail_network_retirement BEFORE INSERT ON networks_ovn_nic_cleanup_retirement BEGIN SELECT RAISE(ABORT,'retirement failure'); END")
					return err
				}

				return nil
			}))
			ctx := context.Background()
			if name == "nil-context" {
				ctx = nil
			}

			generation, id := a.Generation, opts.InstanceUUID
			if name == "wrong-generation" {
				generation = uuid.NewString()
			}

			if name == "missing-generation" {
				generation = ""
			}

			if name == "missing-source" {
				id = uuid.NewString()
			}

			cleared, err := n.InstanceDevicePortStopRetire(ctx, id, opts.DeviceName, generation)
			success := name == "matching" || name == "moved"
			if success {
				require.NoError(t, err)
				require.Equal(t, name == "matching", cleared)
			} else {
				require.Error(t, err)
				require.False(t, cleared)
			}

			require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				record, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
				require.NoError(t, err)
				require.False(t, record.Completed)
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, n.ID()))
				var receipts int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_nic_cleanup_retirement WHERE generation=?", a.Generation).Scan(&receipts))
				if success {
					require.Equal(t, 1, receipts)
				} else {
					require.Zero(t, receipts)
				}

				var host string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'),'')", opts.InstanceID).Scan(&host))
				switch name {
				case "matching":
					require.Empty(t, host)
				default:
					require.Equal(t, "original-host", host)
				}
				// The inherited caller capability survives SQL-only retirement/release.
				var gotToken string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT token FROM networks_ovn_operations WHERE name='source-nic-cleanup'").Scan(&gotToken))
				require.Equal(t, tokens[n.ID()], gotToken)
				return nil
			}))
		})
	}
}

func TestNICStopSourceOVSIdentity(t *testing.T) {
	for _, name := range []string{"stored-and-detached", "missing-plan", "wrong-nb-binding", "incomplete-plan", "nested-with-plan", "nested-no-plan"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			opts.DeviceConfig["type"] = "nic"
			_, err := n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
			require.ErrorIs(t, err, sql.ErrNoRows)
			original := *opts.OVS
			switch name {
			case "missing-plan":
				opts.OVS = nil
			case "wrong-nb-binding":
				opts.OVS.OVNPortName = "replacement-port"
			case "incomplete-plan":
				opts.OVS.InterfaceUUID = ""
			case "nested-with-plan":
				opts.DeviceConfig["nested"] = "parent"
			case "nested-no-plan":
				opts.DeviceConfig["nested"] = "parent"
				opts.OVS = nil
			}

			a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			if name != "stored-and-detached" && name != "nested-no-plan" {
				require.Error(t, err)
				require.Empty(t, a.Generation)
				stored, debt, readErr := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, readErr)
				require.Nil(t, stored)
				require.Nil(t, debt)
				return
			}

			require.NoError(t, err)
			if opts.OVS != nil {
				opts.OVS.InterfaceName = "changed-caller"
			}

			view, err := n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
			require.NoError(t, err)
			if name == "nested-no-plan" {
				require.Nil(t, view.OVS)
				return
			}

			require.Equal(t, original, *view.OVS)
			view.OVS.InterfaceName = "changed-view"
			driverView, networkID, err := OVNNICCleanupSource(a, n.state.DB.Cluster.GetNodeID(), n.state.ServerName)
			require.NoError(t, err)
			require.Equal(t, n.ID(), networkID)
			require.Equal(t, original, *driverView.OVS)
			driverView.OVS.InterfaceName = "changed-driver-view"
			again, err := n.InstanceDevicePortStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
			require.NoError(t, err)
			require.Equal(t, original, *again.OVS)
		})
	}
}

func TestNICStopAddressSetCaptureRequiredBeforeEffects(t *testing.T) {
	for _, name := range []string{"missing-capture", "foreign-root", "wrong-network-name", "altered-removal", "invalid-row", "captured-fields"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			switch name {
			case "missing-capture":
				source.AddressSets = nil
			case "foreign-root":
				source.AddressSets.RootUUID = uuid.NewString()
			case "wrong-network-name":
				source.AddressSets.Rows[0].Name = "sibling_ip4"
			case "altered-removal":
				source.AddressSets.Rows[0].Ownership.Prefixes = []string{"198.51.100.99/32"}
			case "invalid-row":
				source.AddressSets.Rows[0].UUID = "invalid"
			}

			entered := false
			err := stopNICCapturedRoutes(context.Background(), &nicStopRoutesFixture{t: t}, nil,
				func([]networkOVN.NICRouteCleanup) error {
					_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
					return err
				},
				func() error { entered = true; return nil }, func() error { return nil })
			switch name {
			case "captured-fields":
				require.NoError(t, err)
				require.True(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Equal(t, source.AddressSets, stored.AddressSets)
			default:
				require.Error(t, err)
				require.False(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Nil(t, stored)
			}
		})
	}
}

func TestNICStopARPProxyCaptureRequiredBeforeEffects(t *testing.T) {
	for _, name := range []string{"missing-capture", "foreign-root", "wrong-port", "invalid-parent", "altered-removal", "not-applicable", "captured-options"} {
		t.Run(name, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			source.Uplink = true
			source.ARPPrefixes = source.Prefixes
			source.ARPProxy = &networkOVN.NICARPProxyCleanup{
				Version: 2, RootUUID: source.Port.RootUUID, SwitchUUID: uuid.NewString(), SwitchName: "original-ext-switch",
				PortUUID: uuid.NewString(), PortVersion: uuid.NewString(), PortName: source.ProxyPort,
				Ledger: strings.Repeat("0", 64), RouterPort: "original-router", Ownership: source.AddressSets.Rows[0].Ownership,
			}

			switch name {
			case "missing-capture":
				source.ARPProxy = nil
			case "foreign-root":
				source.ARPProxy.RootUUID = uuid.NewString()
			case "wrong-port":
				source.ARPProxy.PortName = "sibling-proxy"
			case "invalid-parent":
				source.ARPProxy.SwitchUUID = ""
			case "altered-removal":
				source.ARPProxy.Ownership.Prefixes = []string{"198.51.100.99/32"}
			case "not-applicable":
				source.Uplink = false
			}

			entered := false
			err := stopNICCapturedRoutes(context.Background(), &nicStopRoutesFixture{t: t}, nil,
				func([]networkOVN.NICRouteCleanup) error {
					_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
					return err
				},
				func() error { entered = true; return nil }, func() error { return nil })
			switch name {
			case "captured-options":
				require.NoError(t, err)
				require.True(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Equal(t, source.ARPProxy, stored.ARPProxy)
			default:
				require.Error(t, err)
				require.False(t, entered)
				stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Nil(t, stored)
			}
		})
	}
}

func TestNICPrefixStartDebtGuardBeforeEffects(t *testing.T) {
	n, opts, source, tokens := nicStopSourceFixture(t)
	n.project, n.name = api.ProjectDefaultName, "source-nic-cleanup"
	_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.NoError(t, err)
	_, _, err = n.instanceDevicePortStart(&OVNInstanceNICSetupOpts{InstanceUUID: opts.InstanceUUID, DeviceName: opts.DeviceName, DeviceConfig: opts.DeviceConfig}, nil, nil)
	require.ErrorContains(t, err, "unacknowledged source OVN NIC cleanup")
	require.Nil(t, n.ovnnb)
	require.Nil(t, n.ovnsb)
	stored, _, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
	require.NoError(t, err)
	require.Equal(t, source.AddressSets, stored.AddressSets)
}

func TestNICStopTerminalCompletionRetry(t *testing.T) {
	n, opts, source, tokens := nicStopSourceFixture(t)
	n.project, n.name = api.ProjectDefaultName, "source-nic-cleanup"
	a, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
	require.NoError(t, err)
	require.ErrorContains(t, n.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation), "lack durable successful host-hook")
	_, err = n.InstanceDevicePortStopRetire(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation)
	require.NoError(t, err)
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `CREATE TRIGGER reject_nic_terminal BEFORE UPDATE OF completed ON networks_ovn_nic_cleanup BEGIN SELECT RAISE(ABORT, 'injected terminal acknowledgment failure'); END`)
		return err
	}))
	err = n.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation)
	require.ErrorContains(t, err, "injected terminal acknowledgment failure")
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, n.ID()))
		receipt, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
		require.NoError(t, err)
		require.False(t, receipt.Completed)
		_, err = tx.Tx().ExecContext(ctx, `DROP TRIGGER reject_nic_terminal`)
		return err
	}))
	require.Error(t, n.InstanceDevicePortStopComplete(context.Background(), uuid.NewString(), a.DeviceName, a.Generation))
	require.NoError(t, n.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation))
	require.NoError(t, n.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation))
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, n.ID()))
		receipt, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
		require.NoError(t, err)
		require.True(t, receipt.Completed)
		require.Equal(t, a.Payload, receipt.Payload)
		return nil
	}))
}

func TestNICStopSourceEffectFencePlacementAuthority(t *testing.T) {
	for _, mode := range []string{"original-local-missing-backend", "original-local-wrong-root", "missing-instance", "moved-without-stage", "replaced-uuid", "wrong-generation"} {
		t.Run(mode, func(t *testing.T) {
			n, opts, snapshot, tokens := nicStopSourceFixture(t)
			n.project, n.name = api.ProjectDefaultName, "source-nic-cleanup"
			if mode == "original-local-wrong-root" {
				n.ovnnb, n.ovnsb = &networkOVN.NB{}, &networkOVN.SB{}
			}

			ctx := context.Background()
			a, err := n.publishNICStopSource(ctx, opts, snapshot, tokens)
			require.NoError(t, err)
			if mode != "missing-instance" {
				require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					node := tx.GetNodeID()
					if mode == "moved-without-stage" {
						var err error
						node, err = tx.CreateNode("moved-target", "192.0.2.55:8443")
						if err != nil {
							return err
						}
					}

					_, err := tx.Tx().ExecContext(ctx, `INSERT INTO instances(id,node_id,name,architecture,type,description,project_id) VALUES (42,?,'original-effect-fence',1,0,'',(SELECT id FROM projects WHERE name='default'))`, node)
					if err != nil {
						return err
					}

					identity := opts.InstanceUUID
					if mode == "replaced-uuid" {
						identity = uuid.NewString()
					}

					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (42,'volatile.uuid',?)", identity)
					return err
				}))
			}

			generation := a.Generation
			if mode == "wrong-generation" {
				generation = uuid.NewString()
			}

			release, err := n.InstanceDevicePortStopFence(ctx, opts.InstanceUUID, opts.DeviceName, generation)
			require.Error(t, err)
			require.Nil(t, release)
			switch mode {
			case "original-local-missing-backend":
				require.ErrorContains(t, err, "backend preflight client is unavailable")
			case "original-local-wrong-root":
				require.ErrorContains(t, err, "Original NIC port cleanup identity is invalid or changed")
			}

			require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				pending, err := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
				if err != nil {
					return err
				}

				require.False(t, pending.Completed)
				return nil
			}))
		})
	}
}

func TestNICStopSourceCapturedPrefixCandidates(t *testing.T) {
	for _, mode := range []string{"empty-published", "outside-original-candidates", "wrong-family"} {
		t.Run(mode, func(t *testing.T) {
			n, opts, source, tokens := nicStopSourceFixture(t)
			source.AddressSets.Rows[0].Ownership.Prefixes = []string{}
			source.Uplink = true
			source.ARPPrefixes = append([]net.IPNet{}, source.Prefixes...)
			source.ARPProxy = &networkOVN.NICARPProxyCleanup{Version: 2, RootUUID: source.Port.RootUUID, SwitchUUID: uuid.NewString(), SwitchName: "original-ext-switch", PortUUID: uuid.NewString(), PortVersion: uuid.NewString(), PortName: source.ProxyPort, Ledger: strings.Repeat("0", 64), RouterPort: "original-router", Ownership: source.AddressSets.Rows[0].Ownership}
			if mode == "outside-original-candidates" {
				source.AddressSets.Rows[0].Ownership.Prefixes = []string{"192.0.2.99/32"}
			}

			if mode == "wrong-family" {
				source.AddressSets.Rows[0].Ownership.Prefixes = []string{"2001:db8::1/128"}
				source.Prefixes = append(source.Prefixes, IPToNet(net.ParseIP("2001:db8::1")))
			}

			_, err := n.publishNICStopSource(context.Background(), opts, source, tokens)
			switch mode {
			case "empty-published":
				require.NoError(t, err)
				stored, attempt, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.NotNil(t, attempt)
				require.Empty(t, stored.AddressSets.Rows[0].Ownership.Prefixes)
				require.Empty(t, stored.ARPProxy.Ownership.Prefixes)
				require.Equal(t, source.Prefixes, stored.Prefixes)
			default:
				require.Error(t, err)
				stored, attempt, err := n.pendingNICStopSource(context.Background(), opts.InstanceUUID, opts.DeviceName)
				require.NoError(t, err)
				require.Nil(t, stored)
				require.Nil(t, attempt)
			}
		})
	}
}
