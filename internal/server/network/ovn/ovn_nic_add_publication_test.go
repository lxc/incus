package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovnNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

func nicAddTestOpts(t *testing.T) *OVNSwitchPortOpts {
	t.Helper()
	mac, err := net.ParseMAC("10:66:6a:93:01:99")
	require.NoError(t, err)
	enabled := false
	return &OVNSwitchPortOpts{MAC: mac, Enabled: &enabled}
}

func TestNICAddAllocationPredicate(t *testing.T) {
	mac := "10:66:6a:93:01:99"
	for _, tc := range []struct {
		name    string
		value   any
		dynamic bool
		ipam    map[string]string
		ready   bool
		err     bool
	}{
		{"assigned-ipv4", mac + " 10.223.70.3", true, map[string]string{"subnet": "10.223.70.0/24"}, true, false},
		{"pending-ipv4", nicCleanupStringSetWire(nil), true, map[string]string{"subnet": "10.223.70.0/24"}, false, false},
		{"ipv4-IPAM-without-DHCP-options", mac + " 10.223.70.3", true, map[string]string{"subnet": "10.223.70.0/24"}, true, false},
		{"no-IPAM-default-empty", nicCleanupStringSetWire(nil), true, map[string]string{}, true, false},
		{"no-IPAM-mac-only-false", nicCleanupStringSetWire(nil), true, map[string]string{"mac_only": "false"}, true, false},
		{"no-IPAM-stale-mac-waits-clear", mac, true, map[string]string{}, false, false},
		{"no-IPAM-mac-only-true-waits", nicCleanupStringSetWire(nil), true, map[string]string{"mac_only": "true"}, false, false},
		{"no-IPAM-mac-only-true-completes", mac, true, map[string]string{"mac_only": "true"}, true, false},
		{"dual-pending-ipv6", mac + " 10.223.70.3", true, map[string]string{"subnet": "10.223.70.0/24", "ipv6_prefix": "fdca:70::/64"}, false, false},
		{"dual-completed", mac + " 10.223.70.3 fdca:70::3", true, map[string]string{"subnet": "10.223.70.0/24", "ipv6_prefix": "fdca:70::/64"}, true, false},
		{"static-cleared", nicCleanupStringSetWire(nil), false, map[string]string{"subnet": "10.223.70.0/24"}, true, false},
		{"static-stale-waits", mac + " 10.223.70.3", false, map[string]string{"subnet": "10.223.70.0/24"}, false, false},
		{"wrong-mac", "02:00:00:00:00:01 10.223.70.3", true, map[string]string{"subnet": "10.223.70.0/24"}, false, true},
		{"wrong-prefix", mac + " 192.0.2.3", true, map[string]string{"subnet": "10.223.70.0/24"}, false, true},
		{"unexpected-family", mac + " fdca:70::3", true, map[string]string{"subnet": "10.223.70.0/24"}, false, true},
		{"invalid-IP", mac + " broken", true, map[string]string{"subnet": "10.223.70.0/24"}, false, true},
		{"duplicate-family", mac + " 10.223.70.3 10.223.70.4", true, map[string]string{"subnet": "10.223.70.0/24"}, false, true},
		{"wrong-IPAM-family", mac + " 10.223.70.3", true, map[string]string{"subnet": "fdca:70::/64"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready, err := nicAddAllocationReady(tc.value, tc.dynamic, mac, tc.ipam)
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.ready, ready)
			}
		})
	}
}

type nicAddTestRecordedClient struct {
	ovsdbClient.Client
	t      *testing.T
	mu     sync.Mutex
	before func([]ovsdb.Operation)
	after  func([]ovsdb.Operation, []ovsdb.OperationResult, error) error
	file   *os.File
}

func (c *nicAddTestRecordedClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		b, err := json.Marshal(ops)
		require.NoError(c.t, err)
		_, err = c.file.Write(append(b, '\n'))
		require.NoError(c.t, err)
	}

	if c.before != nil {
		c.before(ops)
	}

	r, err := c.Client.Transact(ctx, ops...)
	if c.after != nil {
		err = c.after(ops, r, err)
	}

	return r, err
}

func nicAddTestFixture(t *testing.T, opts *OVNSwitchPortOpts, ipam map[string]string) (*NB, ovsdbClient.Client, NICConfigPublication, *nicAddTestRecordedClient) {
	t.Helper()
	nb, raw := referenceTestNB(t)
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "nic-add-test", "other_config": nicCleanupStringMapWire(ipam)}})
	// Wait for this owned fixture's inserted switch to reach the actual client's monitor cache.
	require.Eventually(t, func() bool {
		sw := ovnNB.LogicalSwitch{Name: "nic-add-test"}
		return nb.get(context.Background(), &sw) == nil
	}, time.Second, time.Millisecond)
	require.NoError(t, nb.CreateLogicalSwitchPort(context.Background(), "nic-add-test", "nic-add-port", opts, false))
	p := NICConfigPublication{NetworkID: 17, ProjectID: 1, InstanceUUID: uuid.NewString(), Device: "eth0", Source: "nic-add-source", Generation: uuid.NewString(), Phase: "add", Input: map[string]string{"hwaddr": opts.MAC.String(), "network": "nic-add-test"}, ACLIDs: map[string]int64{}}
	recorder := &nicAddTestRecordedClient{Client: nb.client, t: t}
	dir := os.Getenv("INCUS_TEST_NIC_ADD_EVIDENCE")
	if dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s-operations.jsonl", t.Name(), uuid.NewString())))
		if err != nil { // Test names contain slashes; preserve finite named per-test directories.
			require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(t.Name())), 0o700))
			f, err = os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s-operations.jsonl", t.Name(), uuid.NewString())))
		}

		require.NoError(t, err)
		recorder.file = f
		t.Cleanup(func() { require.NoError(t, f.Close()) })
	}

	nb.client = recorder
	return nb, raw, p, recorder
}

func nicAddTestUpdate(t *testing.T, raw ovsdbClient.Client, row ovsdb.Row) {
	t.Helper()
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-port"}}, Row: row})
}

func nicAddTestPublished(t *testing.T, raw ovsdbClient.Client) (NICConfigPublication, bool) {
	t.Helper()
	r := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-port"}}})
	if len(r[0].Rows) == 0 {
		return NICConfigPublication{}, false
	}

	require.Len(t, r[0].Rows, 1)
	ids, err := nicCleanupStringMap(r[0].Rows[0]["external_ids"])
	require.NoError(t, err)
	if ids[nicConfigPublicationKey] == "" {
		return NICConfigPublication{}, false
	}

	var p NICConfigPublication
	require.NoError(t, json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p))
	return p, true
}

func nicAddTestIsPublication(ops []ovsdb.Operation) bool {
	for _, op := range ops {
		if op.Op == ovsdb.OperationUpdate && op.Table == "Logical_Switch_Port" {
			_, ok := op.Row["external_ids"]
			if ok {
				return true
			}
		}
	}

	return false
}

func TestNICAddRealRPCRequestNormalization(t *testing.T) {
	for _, tc := range []struct {
		name     string
		v4, v6   string
		promisc  bool
		ipam     map[string]string
		assigned string
	}{
		{"no-IPAM-default", "", "", false, map[string]string{}, ""},
		{"mac-only", "", "", false, map[string]string{"mac_only": "true"}, "10:66:6a:93:01:99"},
		{"disabled-both", "none", "none", false, map[string]string{"subnet": "10.223.70.0/24"}, ""},
		{"disabled-promiscuous", "none", "none", true, map[string]string{}, ""},
		{"dynamic-promiscuous", "", "", true, map[string]string{"subnet": "10.223.70.0/24"}, "10:66:6a:93:01:99 10.223.70.3"},
		{"static-v4-EUI64", "10.223.70.19", "fdca:70::1266:6aff:fe93:199", false, map[string]string{"subnet": "10.223.70.0/24", "ipv6_prefix": "fdca:70::/64"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := nicAddTestOpts(t)
			opts.IPV4, opts.IPV6, opts.Promiscuous = tc.v4, tc.v6, tc.promisc
			nb, raw, p, _ := nicAddTestFixture(t, opts, tc.ipam)
			if tc.assigned != "" {
				nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": tc.assigned})
			}

			require.NoError(t, nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts))
			published, ok := nicAddTestPublished(t, raw)
			require.True(t, ok)
			want, _, err := nicAddAddresses(opts)
			require.NoError(t, err)
			rows := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-port"}}})
			actual, err := nicCleanupStringSet(rows[0].Rows[0]["addresses"])
			require.NoError(t, err)
			require.Equal(t, want, actual)
			wantSettings, err := json.Marshal(publicationSettings(rows[0].Rows[0]))
			require.NoError(t, err)
			actualSettings, err := json.Marshal(published.Settings)
			require.NoError(t, err)
			require.JSONEq(t, string(wantSettings), string(actualSettings))
		})
	}
}

func TestNICAddRealRPCAsyncAndStaticClear(t *testing.T) {
	for _, static := range []bool{false, true} {
		t.Run(fmt.Sprintf("static-%t", static), func(t *testing.T) {
			opts := nicAddTestOpts(t)
			if static {
				opts.IPV4 = "10.223.70.19"
			}

			nb, raw, p, hook := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
			if static {
				nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": "10:66:6a:93:01:99 10.223.70.3"})
			}

			reads := 0
			hook.before = func(ops []ovsdb.Operation) {
				for _, op := range ops {
					if op.Op == ovsdb.OperationSelect && op.Table == "Logical_Switch_Port" {
						reads++
						if reads == 2 {
							value := any("10:66:6a:93:01:99 10.223.70.3")
							if static {
								value = nicCleanupStringSetWire(nil)
							}

							nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": value})
						}
					}
				}
			}

			require.NoError(t, nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts))
			require.Equal(t, 2, reads)
			published, ok := nicAddTestPublished(t, raw)
			require.True(t, ok)
			if static {
				value, err := json.Marshal(published.Settings["dynamic_addresses"])
				require.NoError(t, err)
				require.JSONEq(t, `["set",[]]`, string(value))
			} else {
				require.Equal(t, "10:66:6a:93:01:99 10.223.70.3", published.Settings["dynamic_addresses"])
			}
		})
	}
}

func TestNICAddRealRPCImmutableDrift(t *testing.T) {
	for _, column := range []string{"addresses", "enabled", "external_ids", "port_security", "other_config", "ports", "root", "port-replaced"} {
		t.Run(column, func(t *testing.T) {
			opts := nicAddTestOpts(t)
			nb, raw, p, hook := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
			reads := 0
			hook.before = func(ops []ovsdb.Operation) {
				for _, op := range ops {
					if op.Op == ovsdb.OperationSelect && op.Table == "Logical_Switch_Port" {
						reads++
						if reads != 2 {
							return
						}

						switch column {
						case "other_config":
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-test"}}, Row: ovsdb.Row{"other_config": nicCleanupStringMapWire(map[string]string{"subnet": "192.0.2.0/24"})}})
						case "ports":
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-test"}}, Row: ovsdb.Row{"ports": nicCleanupStringSetWire(nil)}})
						case "port-replaced":
							old := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-port"}}})[0].Rows[0]
							delete(old, "_uuid")
							delete(old, "_version")
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-port"}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "newport", Row: old}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-test"}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "newport"}}}}})
						case "root":
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: nb.backendID}}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})
						default:
							var v any
							switch column {
							case "addresses":
								v = "02:00:00:00:00:01 dynamic"
							case "enabled":
								v = true
							case "external_ids":
								v = nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "nic-add-test", "foreign-owner": "changed"})
							case "port_security":
								v = "10:66:6a:93:01:99 10.223.70.3"
							}

							nicAddTestUpdate(t, raw, ovsdb.Row{column: v})
						}
					}
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			require.Error(t, nb.PublishNICAddConfig(ctx, "nic-add-test", "nic-add-port", p, opts))
			_, ok := nicAddTestPublished(t, raw)
			require.False(t, ok)
		})
	}
}

func TestNICAddRealRPCAtomicIPAMAndPublicationFailure(t *testing.T) {
	for _, kind := range []string{"ipam", "owner", "enabled", "dynamic-transition", "lost-reply", "lost-reply-owner", "lost-reply-IPAM"} {
		t.Run(kind, func(t *testing.T) {
			opts := nicAddTestOpts(t)
			nb, raw, p, hook := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
			nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": "10:66:6a:93:01:99 10.223.70.3"})
			dispatch := 0
			hook.before = func(ops []ovsdb.Operation) {
				if !nicAddTestIsPublication(ops) {
					return
				}

				dispatch++
				switch kind {
				case "ipam":
					referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-test"}}, Row: ovsdb.Row{"other_config": nicCleanupStringMapWire(map[string]string{"subnet": "192.0.2.0/24"})}})
				case "owner":
					nicAddTestUpdate(t, raw, ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusSwitch: "nic-add-test", "foreign-owner": "changed"})})
				case "enabled":
					nicAddTestUpdate(t, raw, ovsdb.Row{"enabled": true})
				case "dynamic-transition":
					nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": "10:66:6a:93:01:99 10.223.70.4"})
				}
			}

			hook.after = func(ops []ovsdb.Operation, r []ovsdb.OperationResult, err error) error {
				if (kind == "lost-reply" || kind == "lost-reply-owner" || kind == "lost-reply-IPAM") && nicAddTestIsPublication(ops) && err == nil {
					if kind == "lost-reply-owner" {
						nicAddTestUpdate(t, raw, ovsdb.Row{"enabled": true})
					}

					if kind == "lost-reply-IPAM" {
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "nic-add-test"}}, Row: ovsdb.Row{"other_config": nicCleanupStringMapWire(map[string]string{"subnet": "192.0.2.0/24"})}})
					}

					return errors.New("modeled reply lost after actual commit")
				}

				return err
			}

			err := nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts)
			published, ok := nicAddTestPublished(t, raw)
			require.Equal(t, 1, dispatch)
			switch kind {
			case "lost-reply":
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, p.Generation, published.Generation)
			case "lost-reply-owner", "lost-reply-IPAM":
				require.Error(t, err)
				require.True(t, ok) // actual first commit occurred; changed authority forbids acknowledging success.
			default:
				require.Error(t, err)
				require.False(t, ok)
			}
		})
	}
}

func TestNICAddContextAndBackendFailures(t *testing.T) {
	opts := nicAddTestOpts(t)
	nb, raw, p, hook := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, nb.PublishNICAddConfig(ctx, "nic-add-test", "nic-add-port", p, opts), context.DeadlineExceeded)
	_, ok := nicAddTestPublished(t, raw)
	require.False(t, ok)
	cancelled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	require.ErrorIs(t, nb.PublishNICAddConfig(cancelled, "nic-add-test", "nic-add-port", p, opts), context.Canceled)
	failure := errors.New("owned backend unavailable")
	hook.after = func(_ []ovsdb.Operation, _ []ovsdb.OperationResult, _ error) error { return failure }
	require.ErrorIs(t, nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts), failure)
}

func TestNICAddRealRPCBoundAndDisconnectedBackend(t *testing.T) {
	t.Run("max-read-time-bound", func(t *testing.T) {
		opts := nicAddTestOpts(t)
		nb, raw, p, hook := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
		reads := 0
		hook.before = func(ops []ovsdb.Operation) {
			for _, op := range ops {
				if op.Op == ovsdb.OperationSelect && op.Table == "Logical_Switch_Port" {
					reads++
				}
			}
		}

		start := time.Now()
		err := nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts)
		require.Error(t, err)
		require.LessOrEqual(t, reads, 200)
		require.GreaterOrEqual(t, reads, 2)
		require.Less(t, time.Since(start), 11*time.Second)
		_, ok := nicAddTestPublished(t, raw)
		require.False(t, ok)
		t.Logf("bounded pending Add reads=%d elapsed=%s error=%v", reads, time.Since(start), err)
	})
	t.Run("disconnected-owned-backend", func(t *testing.T) {
		opts := nicAddTestOpts(t)
		nb, raw, p, _ := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
		nb.client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		require.Error(t, nb.PublishNICAddConfig(ctx, "nic-add-test", "nic-add-port", p, opts))
		_, ok := nicAddTestPublished(t, raw)
		require.False(t, ok)
	})
}

// TestNICProducerTransferRealBackend covers handing a completed original producer of another
// member to the caller after a cold move, and every refusal of that handover.
func TestNICProducerTransferRealBackend(t *testing.T) {
	for _, mode := range []string{"ok", "wrong-from", "enabled", "up", "pending", "identity"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw, p, _ := nicAddTestFixture(t, nicAddTestOpts(t), map[string]string{})
			require.NoError(t, nb.PublishNICConfig(ctx, "nic-add-test", "nic-add-port", p))
			switch mode {
			case "enabled":
				nicAddTestUpdate(t, raw, ovsdb.Row{"enabled": ovsdb.OvsSet{GoSet: []any{true}}})
			case "up":
				nicAddTestUpdate(t, raw, ovsdb.Row{"up": ovsdb.OvsSet{GoSet: []any{true}}})
			case "pending":
				require.NoError(t, nb.BeginNICConfigPublication(ctx, "nic-add-test", "nic-add-port", p))
			}

			target := p
			target.Source = "nic-add-target"
			if mode == "identity" {
				target.InstanceUUID = uuid.NewString()
			}

			err := nb.BeginNICConfigPublication(ctx, "nic-add-test", "nic-add-port", target)
			var moved *NICProducerSourceError
			if mode == "identity" {
				require.Error(t, err)
				require.False(t, errors.As(err, &moved))
				return
			}

			require.ErrorAs(t, err, &moved)
			require.Equal(t, "nic-add-source", moved.Source)
			before, ok := nicAddTestPublished(t, raw)
			require.True(t, ok)
			from := moved.Source
			if mode == "wrong-from" {
				from = "someone-else"
			}

			err = nb.BeginNICConfigPublicationTransfer(ctx, "nic-add-test", "nic-add-port", target, from)
			after, ok := nicAddTestPublished(t, raw)
			require.True(t, ok)
			if mode != "ok" {
				require.Error(t, err)
				require.Equal(t, before, after)
				return
			}

			require.NoError(t, err)
			source, err := nb.NICConfigPublicationSource(ctx, "nic-add-test", "nic-add-port")
			require.NoError(t, err)
			require.Equal(t, "nic-add-target", source)
			source, err = nb.NICConfigPublicationSource(ctx, "nic-add-test", "absent-port")
			require.NoError(t, err)
			require.Empty(t, source)
			require.Equal(t, "nic-add-target", after.Source)
			require.Equal(t, "pending", after.Phase)
			require.Equal(t, before.PortUUID, after.PortUUID)
			require.NoError(t, nb.BeginNICConfigPublication(ctx, "nic-add-test", "nic-add-port", target))
		})
	}
}

// TestNICRetirementIgnoresDynamicAddressChurn covers northd reassigning a published port's dynamic
// address: retirement still identifies the exact producer, while producer-written fields stay pinned.
func TestNICRetirementIgnoresDynamicAddressChurn(t *testing.T) {
	for _, mode := range []string{"dynamic-changed", "addresses-changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw, p, _ := nicAddTestFixture(t, nicAddTestOpts(t), map[string]string{})
			require.NoError(t, nb.PublishNICConfig(ctx, "nic-add-test", "nic-add-port", p))
			if mode == "dynamic-changed" {
				nicAddTestUpdate(t, raw, ovsdb.Row{"dynamic_addresses": "10:66:6a:93:01:99 10.223.70.9"})
			} else {
				nicAddTestUpdate(t, raw, ovsdb.Row{"addresses": ovsdb.OvsSet{GoSet: []any{"10:66:6a:93:01:99 10.223.70.10"}}})
			}

			err := nb.RetireNICConfig(ctx, "nic-add-test", "nic-add-port", p, false)
			_, exists := nicAddTestPublished(t, raw)
			if mode == "dynamic-changed" {
				require.NoError(t, err)
				require.False(t, exists)
				return
			}

			require.ErrorContains(t, err, "NIC generated backend settings changed")
			require.True(t, exists)
		})
	}
}

// TestNICConfigLegacyPortAdoption covers an upstream-created port without producer evidence: only
// its exact instance device adopts it, and retirement takes that device as its producer.
func TestNICConfigLegacyPortAdoption(t *testing.T) {
	for _, mode := range []string{"adopt", "foreign-device", "active-elsewhere", "retire", "retire-active"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, raw := referenceTestNB(t)
			referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "incus-net17-ls-int"}})
			require.Eventually(t, func() bool {
				sw := ovnNB.LogicalSwitch{Name: "incus-net17-ls-int"}
				return nb.get(context.Background(), &sw) == nil
			}, time.Second, time.Millisecond)

			target := NICConfigPublication{NetworkID: 17, ProjectID: 1, InstanceUUID: uuid.NewString(), Device: "eth0", Source: "member1", Input: map[string]string{"network": "net"}, ACLIDs: map[string]int64{}}
			name := OVNSwitchPort(fmt.Sprintf("incus-net17-instance-%s-eth0", target.InstanceUUID))
			opts := nicAddTestOpts(t)
			opts.Location = "member1"
			if mode == "active-elsewhere" || mode == "retire-active" {
				enabled := true
				opts.Enabled = &enabled
			}

			if mode == "active-elsewhere" {
				opts.Location = "member2"
			}

			require.NoError(t, nb.CreateLogicalSwitchPort(ctx, "incus-net17-ls-int", name, opts, false))
			published := func() (NICConfigPublication, bool) {
				r := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(name)}}})
				if len(r[0].Rows) == 0 {
					return NICConfigPublication{}, false
				}

				ids, err := nicCleanupStringMap(r[0].Rows[0]["external_ids"])
				require.NoError(t, err)
				p, err := decodeNICConfig(ids[nicConfigPublicationKey])
				return p, err == nil
			}

			switch mode {
			case "adopt":
				require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", name, target))
				p, ok := published()
				require.True(t, ok)
				require.True(t, p.Legacy)
				require.Equal(t, "pending", p.Phase)
				require.Equal(t, p.PortUUID, p.Generation)
				require.Equal(t, target.InstanceUUID, p.InstanceUUID)
				// The adopted producer then follows the ordinary retry identity checks.
				require.NoError(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", name, target))
			case "foreign-device":
				other := target
				other.Device = "eth1"
				require.ErrorContains(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", name, other), "legacy adoption is unsupported")
				require.ErrorContains(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", name), "legacy adoption is unsupported")
				_, ok := published()
				require.False(t, ok)
			case "active-elsewhere":
				require.ErrorContains(t, nb.BeginNICConfigPublication(ctx, "incus-net17-ls-int", name, target), "active on another member")
				_, ok := published()
				require.False(t, ok)
			case "retire":
				require.NoError(t, nb.RetireNICConfig(ctx, "incus-net17-ls-int", name, target, false))
				r := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(name)}}})
				require.Empty(t, r[0].Rows)
			case "retire-active":
				require.ErrorContains(t, nb.RetireNICConfig(ctx, "incus-net17-ls-int", name, target, false), "must complete Stop")
			}
		})
	}
}

// TestNICAddPublicationDerivedColumnRace covers northd writing a new port's derived columns between
// the Add snapshot and its publication: the publication retries, while a pinned change still fails.
func TestNICAddPublicationDerivedColumnRace(t *testing.T) {
	for _, mode := range []string{"up", "pinned"} {
		t.Run(mode, func(t *testing.T) {
			opts := nicAddTestOpts(t)
			opts.IPV4 = "10.223.70.19"
			nb, raw, p, recorder := nicAddTestFixture(t, opts, map[string]string{"subnet": "10.223.70.0/24"})
			raced := false
			recorder.before = func(ops []ovsdb.Operation) {
				if raced || !nicAddTestIsPublication(ops) {
					return
				}

				raced = true
				row := ovsdb.Row{"up": ovsdb.OvsSet{GoSet: []any{false}}}
				if mode == "pinned" {
					row = ovsdb.Row{"addresses": ovsdb.OvsSet{GoSet: []any{"10:66:6a:93:01:99 10.223.70.20"}}}
				}

				nicAddTestUpdate(t, raw, row)
			}

			err := nb.PublishNICAddConfig(context.Background(), "nic-add-test", "nic-add-port", p, opts)
			require.True(t, raced)
			published, ok := nicAddTestPublished(t, raw)
			if mode == "pinned" {
				require.Error(t, err)
				require.False(t, ok)
				return
			}

			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "add", published.Phase)
		})
	}
}

// TestNICProducerSettingsEqual covers producer settings recorded with and without northd-owned
// dynamic addresses, which a multi-port shared reload compares after earlier ports completed.
func TestNICProducerSettingsEqual(t *testing.T) {
	base := map[string]any{"addresses": "10:66:6a:2e:17:5d dynamic", "port_security": []any{"set", []any{}}}
	withDynamic := map[string]any{"addresses": "10:66:6a:2e:17:5d dynamic", "dynamic_addresses": "10:66:6a:2e:17:5d 10.238.0.2", "port_security": []any{"set", []any{}}}
	require.True(t, nicProducerSettingsEqual(base, withDynamic))
	changed := map[string]any{"addresses": "10:66:6a:2e:17:5d 10.238.0.9", "port_security": []any{"set", []any{}}}
	require.False(t, nicProducerSettingsEqual(base, changed))
}
