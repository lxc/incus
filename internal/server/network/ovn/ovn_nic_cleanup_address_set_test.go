package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

// Only the existing in-memory transaction evaluator is constructed.
func nicAddressSetCleanupFixture(t *testing.T) (*NB, *nicRouteCleanupClient, []net.IPNet) {
	root := uuid.NewString()
	_, ipv4, _ := net.ParseCIDR("198.51.100.0/24")
	_, ipv6, _ := net.ParseCIDR("2001:db8::/64")
	rows := []ovsdb.Row{
		{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original_ip4", "addresses": nicCleanupStringSetWire([]string{"203.0.113.0/24"}), "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"foreign": "untouched"}}},
		{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original_ip6", "addresses": nicCleanupStringSetWire([]string{"2001:db8:1::/64"}), "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"foreign": "untouched"}}},
	}

	client := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"NB_Global": {{"_uuid": ovsdb.UUID{GoUUID: root}}}, "Address_Set": rows,
	}}
	nb := &NB{client: client, backendID: root}
	addresses := []net.IPNet{*ipv4, *ipv6}
	nicPrefixTestPublish(t, nb, client, addresses, "", "")
	return nb, client, addresses
}

func captureNICAddressSetTest(t *testing.T, nb *NB, addresses []net.IPNet) NICAddressSetCleanup {
	t.Helper()
	plan, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, nicPrefixTestCapturedOwner(t, nb))
	require.NoError(t, err)
	return plan
}

func TestNICAddressSetCleanupStoredReplay(t *testing.T) {
	nb, client, addresses := nicAddressSetCleanupFixture(t)
	plan := captureNICAddressSetTest(t, nb, addresses)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICAddressSetCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &NB{client: client, backendID: nb.backendID}
	client.afterWriteError = errors.New("inert lost committed address-set reply")
	require.Error(t, fresh.ApplyNICAddressSetCleanup(context.Background(), stored, "original", addresses))
	require.NoError(t, fresh.ApplyNICAddressSetCleanup(context.Background(), stored, "original", addresses))
	for i, row := range client.tables["Address_Set"] {
		current, err := nicCleanupStringSet(row["addresses"])
		require.NoError(t, err)
		require.NotContains(t, current, addresses[i].String())
		ids, err := nicCleanupStringMap(row["external_ids"])
		require.NoError(t, err)
		require.Equal(t, "untouched", ids["foreign"])
	}
}

func TestNICAddressSetCleanupRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "stale-client", "renamed", "new-sibling-entry", "source-delta-changed", "bad-plan"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicAddressSetCleanupFixture(t)
			plan := captureNICAddressSetTest(t, nb, addresses)
			switch name {
			case "root-replaced":
				client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "stale-client":
				nb.backendID = uuid.NewString()
			case "renamed":
				client.tables["Address_Set"][0]["name"] = "foreign"
			case "new-sibling-entry":
				row := client.tables["Address_Set"][0]
				row["addresses"] = nicCleanupStringSetWire([]string{"198.51.100.0/24", "203.0.113.0/24", "192.0.2.0/24"})
				row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "source-delta-changed":
				addresses = nil
			case "bad-plan":
				plan.Rows[1].UUID = plan.Rows[0].UUID
			}

			before := nicARPProxyTables(client.tables)
			require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", addresses))
			require.Equal(t, before, nicARPProxyTables(client.tables))
		})
	}
}

func TestNICAddressSetCleanupDispatchGuards(t *testing.T) {
	for _, name := range []string{"late-replacement", "late-new-prefix", "late-rename", "late-root"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicAddressSetCleanupFixture(t)
			plan := captureNICAddressSetTest(t, nb, addresses)
			client.beforeWrite = func() {
				switch name {
				case "late-replacement":
					client.tables["Address_Set"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "late-new-prefix":
					row := client.tables["Address_Set"][0]
					row["addresses"] = nicCleanupStringSetWire([]string{"198.51.100.0/24", "203.0.113.0/24", "192.0.2.0/24"})
					row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "late-rename":
					client.tables["Address_Set"][0]["name"] = "foreign"
				case "late-root":
					client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				}
			}

			require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", addresses))
			// Both families remain unchanged even when one guard fails.
			for i, row := range client.tables["Address_Set"] {
				current, err := nicCleanupStringSet(row["addresses"])
				require.NoError(t, err)
				require.Contains(t, current, addresses[i].String())
			}
		})
	}
}

func TestNICAddressSetCleanupReplacementPreserved(t *testing.T) {
	nb, client, addresses := nicAddressSetCleanupFixture(t)
	plan := captureNICAddressSetTest(t, nb, addresses)
	replacement := uuid.NewString()
	client.tables["Address_Set"][0]["_uuid"] = ovsdb.UUID{GoUUID: replacement}
	require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", addresses))
	require.Equal(t, replacement, client.tables["Address_Set"][0]["_uuid"].(ovsdb.UUID).GoUUID)
	current, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	require.Contains(t, current, addresses[0].String())
	other, err := nicCleanupStringSet(client.tables["Address_Set"][1]["addresses"])
	require.NoError(t, err)
	require.Contains(t, other, addresses[1].String())
}

func TestNICAddressSetCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"missing-row", "duplicate-name", "invalid-field", "read-error", "short-reply"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicAddressSetCleanupFixture(t)
			owner := nicPrefixTestCapturedOwner(t, nb)
			client.calls = nil
			switch name {
			case "missing-row":
				client.tables["Address_Set"] = client.tables["Address_Set"][:1]
			case "duplicate-name":
				row := ovsdb.Row{}
				for key, value := range client.tables["Address_Set"][0] {
					row[key] = value
				}

				row["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				client.tables["Address_Set"] = append(client.tables["Address_Set"], row)
			case "invalid-field":
				client.tables["Address_Set"][0]["addresses"] = ovsdb.OvsSet{GoSet: []any{42}}
			case "read-error":
				client.failNext = errors.New("inert select failure")
			case "short-reply":
				client.truncateNext = true
			}

			_, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
			require.Error(t, err)
			for _, call := range client.calls {
				for _, op := range call {
					require.NotEqual(t, ovsdb.OperationUpdate, op.Op)
				}
			}
		})
	}
}

// TestNICAddressSetCleanupServerRestart covers a stored plan applied after the database server
// restarted: only the server-local row versions changed, so the cleanup proceeds.
func TestNICAddressSetCleanupServerRestart(t *testing.T) {
	nb, client, addresses := nicAddressSetCleanupFixture(t)
	plan := captureNICAddressSetTest(t, nb, addresses)
	for _, row := range client.tables["Address_Set"] {
		row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	}

	require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", addresses))
}
