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

// Only the existing in-memory RFC7047 transaction evaluator is constructed.
func nicARPProxyCleanupFixture(t *testing.T) (*NB, *nicRouteCleanupClient, []net.IPNet) {
	root, sw, port := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, address, _ := net.ParseCIDR("198.51.100.0/24")
	options := map[string]string{"router-port": "original-router", "arp_proxy": "203.0.113.0/24", "foreign": "retained"}
	client := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"NB_Global":           {{"_uuid": ovsdb.UUID{GoUUID: root}}},
		"Logical_Switch":      {{"_uuid": ovsdb.UUID{GoUUID: sw}, "name": "original-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}}},
		"Logical_Switch_Port": {{"_uuid": ovsdb.UUID{GoUUID: port}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original-proxy", "type": "router", "options": nicCleanupStringMapWire(options), "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"foreign": "retained"}}}},
	}}
	nb := &NB{client: client, backendID: root}
	addresses := []net.IPNet{*address}
	nicPrefixTestPublish(t, nb, client, addresses, "original-switch", "original-proxy")
	return nb, client, addresses
}

func captureNICARPProxyTest(t *testing.T, nb *NB, addresses []net.IPNet) NICARPProxyCleanup {
	t.Helper()
	plan, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, nicPrefixTestCapturedOwner(t, nb))
	require.NoError(t, err)
	return plan
}

func TestNICARPProxyCleanupStoredReplay(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	plan := captureNICARPProxyTest(t, nb, addresses)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICARPProxyCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &NB{client: client, backendID: nb.backendID}
	client.afterWriteError = errors.New("inert lost committed proxy reply")
	require.Error(t, fresh.ApplyNICARPProxyCleanup(context.Background(), stored, "original-proxy", addresses))
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.Equal(t, map[string]string{"router-port": "original-router", "arp_proxy": "203.0.113.0/24", "foreign": "retained"}, options)
	require.NoError(t, fresh.ApplyNICARPProxyCleanup(context.Background(), stored, "original-proxy", addresses))
	ids, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["external_ids"])
	require.NoError(t, err)
	require.Equal(t, "retained", ids["foreign"])
}

func TestNICARPProxyCleanupRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "stale-client", "switch-replaced", "foreign-parent", "renamed", "repurposed", "router-port-changed", "new-sibling-prefix", "altered-delta", "invalid-plan"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			plan := captureNICARPProxyTest(t, nb, addresses)
			switch name {
			case "root-replaced":
				client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "stale-client":
				nb.backendID = uuid.NewString()
			case "switch-replaced":
				client.tables["Logical_Switch"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "foreign-parent":
				client.tables["Logical_Switch"] = append(client.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: plan.PortUUID}}}})
				// Complete the inert row before taking the preservation snapshot.
				client.Schema()
			case "renamed":
				client.tables["Logical_Switch_Port"][0]["name"] = "foreign"
			case "repurposed":
				client.tables["Logical_Switch_Port"][0]["type"] = ""
			case "router-port-changed":
				options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
				require.NoError(t, err)
				options["router-port"] = "foreign-router-port"
				client.tables["Logical_Switch_Port"][0]["options"] = nicCleanupStringMapWire(options)
			case "new-sibling-prefix":
				options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
				require.NoError(t, err)
				options["arp_proxy"] += " 192.0.2.0/24"
				client.tables["Logical_Switch_Port"][0]["options"] = nicCleanupStringMapWire(options)
			case "altered-delta":
				addresses = nil
			case "invalid-plan":
				plan.PortVersion = ""
			}

			before := nicARPProxyTables(client.tables)
			require.Error(t, nb.ApplyNICARPProxyCleanup(context.Background(), plan, "original-proxy", addresses))
			require.Equal(t, before, nicARPProxyTables(client.tables))
		})
	}
}

func TestNICARPProxyCleanupDispatchGuards(t *testing.T) {
	for _, name := range []string{"late-replacement", "late-prefix", "late-owner", "late-root"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			plan := captureNICARPProxyTest(t, nb, addresses)
			client.beforeWrite = func() {
				switch name {
				case "late-replacement":
					client.tables["Logical_Switch_Port"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "late-prefix":
					options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
					require.NoError(t, err)
					options["arp_proxy"] += " 192.0.2.0/24"
					client.tables["Logical_Switch_Port"][0]["options"] = nicCleanupStringMapWire(options)
					client.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "late-owner":
					client.tables["Logical_Switch"] = append(client.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: plan.PortUUID}}}})
				case "late-root":
					client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				}
			}

			require.Error(t, nb.ApplyNICARPProxyCleanup(context.Background(), plan, "original-proxy", addresses))
			options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
			require.NoError(t, err)
			require.Contains(t, options["arp_proxy"], addresses[0].String())
		})
	}
}

func TestNICARPProxyCleanupReplacementPreserved(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	plan := captureNICARPProxyTest(t, nb, addresses)
	replacement := uuid.NewString()
	client.tables["Logical_Switch_Port"][0]["_uuid"] = ovsdb.UUID{GoUUID: replacement}
	client.tables["Logical_Switch"][0]["ports"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: replacement}}}
	require.Error(t, nb.ApplyNICARPProxyCleanup(context.Background(), plan, "original-proxy", addresses))
	require.Equal(t, replacement, client.tables["Logical_Switch_Port"][0]["_uuid"].(ovsdb.UUID).GoUUID)
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.Contains(t, options["arp_proxy"], addresses[0].String())
}

func TestNICARPProxyCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"missing-parent", "missing-port", "duplicate-port", "foreign-parent", "wrong-type", "invalid-options", "read-error", "short-reply"} {
		t.Run(name, func(t *testing.T) {
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			owner := nicPrefixTestCapturedOwner(t, nb)
			client.calls = nil
			switch name {
			case "missing-parent":
				client.tables["Logical_Switch"] = nil
			case "missing-port":
				client.tables["Logical_Switch_Port"] = nil
			case "duplicate-port":
				row := ovsdb.Row{}
				for key, value := range client.tables["Logical_Switch_Port"][0] {
					row[key] = value
				}

				row["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				client.tables["Logical_Switch_Port"] = append(client.tables["Logical_Switch_Port"], row)
			case "foreign-parent":
				port := client.tables["Logical_Switch_Port"][0]["_uuid"]
				client.tables["Logical_Switch"] = append(client.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{port}}})
			case "wrong-type":
				client.tables["Logical_Switch_Port"][0]["type"] = ""
			case "invalid-options":
				client.tables["Logical_Switch_Port"][0]["options"] = ovsdb.OvsMap{GoMap: map[any]any{"router-port": 42}}
			case "read-error":
				client.failNext = errors.New("inert read failure")
			case "short-reply":
				client.truncateNext = true
			}

			_, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, owner)
			require.Error(t, err)
			for _, call := range client.calls {
				for _, op := range call {
					require.NotEqual(t, ovsdb.OperationUpdate, op.Op)
				}
			}
		})
	}
}

// OVSDB maps and singleton sets have multiple equivalent JSON wire encodings.
func nicARPProxyTables(tables map[string][]ovsdb.Row) map[string][]map[string]any {
	result := make(map[string][]map[string]any, len(tables))
	for table, rows := range tables {
		for _, row := range rows {
			normalized := map[string]any{}
			for column, value := range row {
				normalized[column] = nicRouteSetStrings(value)
			}

			result[table] = append(result[table], normalized)
		}
	}

	return result
}

// TestNICARPProxyCleanupServerRestart covers a stored plan applied after the database server restarted.
func TestNICARPProxyCleanupServerRestart(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	plan := captureNICARPProxyTest(t, nb, addresses)
	client.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	require.NoError(t, nb.ApplyNICARPProxyCleanup(context.Background(), plan, "original-proxy", addresses))
}
