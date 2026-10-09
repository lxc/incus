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

func nicMigrationFixture(t *testing.T) (*NB, *nicRouteCleanupClient, NICMigrationShared) {
	t.Helper()
	ctx := context.Background()
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	first := nicPrefixTestCapturedOwner(t, nb)
	sibling := nicPrefixTestOwner(t, nb, client, "sibling")
	require.NoError(t, nb.PublishNICPrefixes(ctx, "original", addresses, "original-switch", "original-proxy", addresses, sibling))
	require.NoError(t, nb.UpdateAddressSetAdd(ctx, "original", addresses...))
	require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(ctx, "original-proxy", addresses, nil))
	dns, router, nat, route, rport, pg := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	client.tables["DNS"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: dns}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "records": nicCleanupStringMapWire(map[string]string{"instance": "192.0.2.9", "foreign": "192.0.2.19"})}}
	for _, sw := range client.tables["Logical_Switch"] {
		sw["dns_records"] = ovsdb.OvsSet{GoSet: []any{}}
		if sw["_uuid"] == (ovsdb.UUID{GoUUID: first.SwitchUUID}) {
			sw["dns_records"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: dns}}}
		}
	}

	client.tables["Logical_Router"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: router}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original-router", "nat": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: nat}}}, "static_routes": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: route}}}, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: rport}}}}}
	client.tables["NAT"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: nat}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "logical_ip": "192.0.2.9", "external_ip": "203.0.113.9", "type": "dnat_and_snat"}}
	client.tables["Logical_Router_Static_Route"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: route}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "ip_prefix": "192.0.2.0/24", "nexthop": "192.0.2.9"}}
	client.tables["Logical_Router_Port"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: rport}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "router-port", "mac": "00:11:22:33:44:55"}}
	client.tables["Port_Group"] = []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: pg}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original-group", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: first.PortUUID}, ovsdb.UUID{GoUUID: sibling.PortUUID}}}, "acls": ovsdb.OvsSet{GoSet: []any{}}}}
	port, err := nb.CaptureNICPortCleanup(ctx, "nic-switch", "nic", "source-member")
	require.NoError(t, err)
	plan, err := nb.CaptureNICMigrationShared(ctx, uuid.NewString(), "target-member", uuid.NewString(), "target-chassis", port, router, "original", addresses, "original-switch", "original-proxy", addresses)
	require.NoError(t, err)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICMigrationShared
	require.NoError(t, json.Unmarshal(wire, &stored))
	return nb, client, stored
}

func TestNICMigrationSharedTransferReplayRollback(t *testing.T) {
	ctx := context.Background()
	nb, client, plan := nicMigrationFixture(t)
	before := nicARPProxyTables(client.tables)
	client.afterWriteError = errors.New("lost committed transfer reply")
	require.NoError(t, nb.ApplyNICMigrationShared(ctx, plan))
	require.NoError(t, nb.ApplyNICMigrationShared(ctx, plan))
	require.NoError(t, nb.VerifyNICMigrationShared(ctx, plan, true))
	for _, table := range []string{"DNS", "NAT", "Logical_Router_Static_Route", "Logical_Router_Port", "Logical_Router", "Port_Group", "Logical_Switch"} {
		require.Equal(t, before[table], nicARPProxyTables(client.tables)[table], table)
	}

	for _, table := range []string{"Address_Set", "Logical_Switch_Port"} {
		for i, row := range client.tables[table] {
			if table == "Address_Set" {
				require.Equal(t, before[table][i]["addresses"], nicRouteSetStrings(row["addresses"]))
			}
		}
	}

	var active NICPrefixOwner
	for _, row := range client.tables["Logical_Switch_Port"] {
		if row["_uuid"] == (ovsdb.UUID{GoUUID: plan.Port.PortUUID}) {
			ids, err := nicCleanupStringMap(row["external_ids"])
			require.NoError(t, err)
			require.Equal(t, "target-member", ids[ovnExtIDIncusLocation])
			require.NoError(t, json.Unmarshal([]byte(ids[nicPrefixGeneration]), &active))
			require.Equal(t, plan.TargetGeneration, active.Generation)
		}
	}

	client.afterWriteError = errors.New("lost committed rollback reply")
	require.NoError(t, nb.RollbackNICMigrationShared(ctx, plan))
	require.NoError(t, nb.RollbackNICMigrationShared(ctx, plan))
	require.NoError(t, nb.VerifyNICMigrationShared(ctx, plan, false))
	after := nicARPProxyTables(client.tables)
	for table, rows := range before {
		for i, row := range rows {
			delete(row, "_version")
			delete(after[table][i], "_version")
		}
	}

	require.Equal(t, before, after)
}

func TestNICMigrationSharedRaceAndReplacementRefusal(t *testing.T) {
	for _, table := range []string{"NB_Global", "Logical_Switch", "Logical_Switch_Port", "DNS", "NAT", "Logical_Router_Static_Route", "Port_Group", "Address_Set"} {
		t.Run(table, func(t *testing.T) {
			nb, client, plan := nicMigrationFixture(t)
			client.beforeWrite = func() { client.tables[table][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()} }
			require.Error(t, nb.ApplyNICMigrationShared(context.Background(), plan))
			for _, row := range client.tables["Logical_Switch_Port"] {
				if row["_uuid"] == (ovsdb.UUID{GoUUID: plan.Port.PortUUID}) {
					ids, _ := nicCleanupStringMap(row["external_ids"])
					require.Equal(t, "source-member", ids[ovnExtIDIncusLocation])
				}
			}
		})
	}

	nb, client, plan := nicMigrationFixture(t)
	require.NoError(t, nb.ApplyNICMigrationShared(context.Background(), plan))
	client.tables["NAT"][0]["external_ip"] = "foreign-replacement"
	before := nicARPProxyTables(client.tables)
	require.Error(t, nb.RollbackNICMigrationShared(context.Background(), plan))
	require.Equal(t, before, nicARPProxyTables(client.tables))
}

func TestNICMigrationTransferredSourceRetryAfterTargetLifecycle(t *testing.T) {
	ctx := context.Background()
	nb, client, plan := nicMigrationFixture(t)
	require.NoError(t, nb.ApplyNICMigrationShared(ctx, plan))
	current, err := nb.CaptureNICPortCleanup(ctx, plan.Port.SwitchName, plan.Port.PortName, "target-member")
	require.NoError(t, err)
	require.NoError(t, nb.ApplyNICPortCleanup(ctx, current))
	require.NoError(t, nb.VerifyNICMigrationTransferred(ctx, plan))
	// A later ordinary target allocation supersedes the transferred generation.
	next, err := nb.NewNICPrefixOwner(ctx, plan.Port.SwitchName, plan.Port.PortName, "target-member")
	require.NoError(t, err)
	_, prefix, _ := net.ParseCIDR("198.51.100.0/24")
	require.NoError(t, nb.PublishNICPrefixes(ctx, "original", []net.IPNet{*prefix}, "original-switch", "original-proxy", []net.IPNet{*prefix}, next))
	require.NoError(t, nb.VerifyNICMigrationTransferred(ctx, plan))
	for _, row := range client.tables["Logical_Switch_Port"] {
		if row["_uuid"] == (ovsdb.UUID{GoUUID: plan.Port.PortUUID}) {
			row["type"] = "router"
		}
	}

	before := nicARPProxyTables(client.tables)
	require.Error(t, nb.VerifyNICMigrationTransferred(ctx, plan))
	require.Equal(t, before, nicARPProxyTables(client.tables))
}

func TestNICMigrationTransferredAbsenceAndRoots(t *testing.T) {
	for _, name := range []string{"absent-port-replaced-name", "root-replacement", "parent-replacement", "same-uuid-rename", "same-generation-address-change", "lost-lineage"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			nb, client, plan := nicMigrationFixture(t)
			require.NoError(t, nb.ApplyNICMigrationShared(ctx, plan))
			switch name {
			case "absent-port-replaced-name":
				for i, row := range client.tables["Logical_Switch_Port"] {
					if row["_uuid"] == (ovsdb.UUID{GoUUID: plan.Port.PortUUID}) {
						client.tables["Logical_Switch_Port"][i]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
					}
				}

			case "root-replacement":
				client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "parent-replacement":
				for _, row := range client.tables["Logical_Switch"] {
					if row["_uuid"] == (ovsdb.UUID{GoUUID: plan.Port.SwitchUUID}) {
						row["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
					}
				}

			default:
				for _, row := range client.tables["Logical_Switch_Port"] {
					if row["_uuid"] != (ovsdb.UUID{GoUUID: plan.Port.PortUUID}) {
						continue
					}

					switch name {
					case "same-uuid-rename":
						row["name"] = "foreign"
					case "same-generation-address-change":
						row["addresses"] = "foreign-mac"
					case "lost-lineage":
						ids, _ := nicCleanupStringMap(row["external_ids"])
						delete(ids, nicPrefixGeneration)
						row["external_ids"] = nicCleanupStringMapWire(ids)
					}
				}
			}

			before := nicARPProxyTables(client.tables)
			err := nb.VerifyNICMigrationTransferred(ctx, plan)
			if name == "absent-port-replaced-name" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			require.Equal(t, before, nicARPProxyTables(client.tables))
		})
	}
}

func TestNICMigrationMACReadOnlyRefreshAndDrift(t *testing.T) {
	for _, name := range []string{"original", "controller-refresh", "old-uuid-obsolete", "foreign-tuple", "foreign-datapath", "root-replacement", "datapath-replacement"} {
		t.Run(name, func(t *testing.T) {
			sb, client, nbRoot, router := nicMACCleanupFixture()
			plan := captureNICMACTest(t, sb, nbRoot, router)
			switch name {
			case "controller-refresh":
				client.tables["MAC_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "old-uuid-obsolete":
				client.tables["MAC_Binding"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "foreign-tuple":
				client.tables["MAC_Binding"][0]["ip"] = "192.0.2.99"
			case "foreign-datapath":
				client.tables["MAC_Binding"][0]["datapath"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "root-replacement":
				client.tables["SB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "datapath-replacement":
				client.tables["Datapath_Binding"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			}

			before := nicARPProxyTables(client.tables)
			err := sb.VerifyNICMigrationMACBindings(context.Background(), plan)
			if name == "original" || name == "controller-refresh" || name == "old-uuid-obsolete" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			require.Equal(t, before, nicARPProxyTables(client.tables))
			for _, call := range client.calls {
				for _, op := range call {
					require.Contains(t, []string{ovsdb.OperationWait, ovsdb.OperationSelect}, op.Op)
				}
			}
		})
	}
}
