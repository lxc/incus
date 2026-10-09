package ovn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovnNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

const (
	nicStartSwitch = OVNSwitch("copy-start-switch")
	nicStartPort   = OVNSwitchPort("copy-start-port")
	nicStartSource = "copy-start-source"
)

func nicStartTestFixture(t *testing.T) (*NB, ovsdbClient.Client, *nicAddTestRecordedClient) {
	t.Helper()
	nb, raw := referenceTestNB(t)
	ctx := context.Background()
	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": string(nicStartSwitch)}})
	require.Eventually(t, func() bool {
		row := ovnNB.LogicalSwitch{Name: string(nicStartSwitch)}
		return nb.get(ctx, &row) == nil
	}, time.Second, time.Millisecond)
	mac, err := net.ParseMAC("00:16:3e:22:33:44")
	require.NoError(t, err)
	enabled := true
	// Copy Add skipped its conflicting static allocation. Start creates the now absent port after
	// ordinary deletion of the original; unlike ordinary Add, this port is immediately enabled.
	require.NoError(t, nb.CreateLogicalSwitchPort(ctx, nicStartSwitch, nicStartPort, &OVNSwitchPortOpts{MAC: mac, IPV4: "192.0.2.2", IPV6: "2001:db8::2", Location: nicStartSource, Enabled: &enabled}, true))
	for _, name := range []string{"copy_start_ip4", "copy_start_ip6"} {
		referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": name}})
	}

	recorder := &nicAddTestRecordedClient{Client: nb.client, t: t}
	nb.client = recorder
	return nb, raw, recorder
}

func nicStartTestChange(t *testing.T, raw ovsdbClient.Client, change string) {
	t.Helper()
	where := []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(nicStartPort)}}
	var before ovsdb.Row
	if change == "up" {
		before = nicStartTestSnapshot(t, raw)
	}

	var row ovsdb.Row
	switch change {
	case "none":
		return
	case "up":
		row = ovsdb.Row{"up": ovsdb.OvsSet{GoSet: []any{false}}}
	case "addresses", "port_security":
		row = ovsdb.Row{change: nicCleanupStringSetWire([]string{"00:16:3e:22:33:44 192.0.2.3 2001:db8::3"})}
	case "dynamic_addresses":
		row = ovsdb.Row{change: "00:16:3e:22:33:44 192.0.2.3 2001:db8::3"}
	case "enabled":
		row = ovsdb.Row{change: false}
	case "name":
		row = ovsdb.Row{change: "renamed-copy"}
	case "parent_name":
		row = ovsdb.Row{change: "other-parent"}
	case "type":
		row = ovsdb.Row{change: "localnet"}
	case "tag", "tag_request":
		row = ovsdb.Row{change: 7}
	case "options":
		row = ovsdb.Row{change: nicCleanupStringMapWire(map[string]string{"requested-chassis": "other-chassis"})}
	case "external_ids", "source", "generation", "producer":
		result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: where, Columns: []string{"external_ids"}})
		ids, err := nicCleanupStringMap(result[0].Rows[0]["external_ids"])
		require.NoError(t, err)
		key, value := "other-producer-field", "changed"
		switch change {
		case "source":
			key, value = ovnExtIDIncusLocation, "other-source"
		case "generation":
			key = nicPrefixGeneration
		case "producer":
			key = nicConfigPublicationKey
		}

		ids[key] = value
		row = ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}
	case "dhcpv4_options", "dhcpv6_options", "ha_chassis_group", "mirror_rules":
		table, data := "DHCP_Options", ovsdb.Row{"cidr": "192.0.2.0/24"}
		switch change {
		case "dhcpv6_options":
			data = ovsdb.Row{"cidr": "2001:db8::/64"}
		case "ha_chassis_group":
			table, data = "HA_Chassis_Group", ovsdb.Row{"name": "other-chassis-group"}
		case "mirror_rules":
			table, data = "Mirror", ovsdb.Row{"name": "other-mirror", "filter": "both", "type": "local", "sink": "other-sink"}
		}

		id := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: data})[0].UUID
		row = ovsdb.Row{change: ovsdb.OvsSet{GoSet: []any{id}}}
	case "replacement":
		result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: where})
		original := result[0].Rows[0]
		id := original["_uuid"]
		delete(original, "_uuid")
		delete(original, "_version")
		referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: where},
			ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "replacement", Row: original},
			ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(nicStartSwitch)}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{id}}}, {Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "replacement"}}}}}})
		return
	case "parent":
		referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(nicStartSwitch)}}, Row: ovsdb.Row{"ports": ovsdb.OvsSet{GoSet: []any{}}}})
		return
	case "root":
		referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionNotEqual, Value: ovsdb.UUID{GoUUID: "00000000-0000-0000-0000-000000000000"}}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})
		return
	default:
		t.Fatalf("Unknown producer change %q", change)
	}

	referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: where, Row: row})
	if change == "up" {
		after := nicStartTestSnapshot(t, raw)
		previous, err := nicStartPortDigest(before)
		require.NoError(t, err)
		current, err := nicStartPortDigest(after)
		require.NoError(t, err)
		require.Equal(t, previous, current)
		require.Equal(t, before["_uuid"], after["_uuid"])
		require.NotEqual(t, before["_version"], after["_version"])
		t.Logf("Native derived-only update: UUID=%v before_version=%v after_version=%v before_up=%v after_up=%v unchanged_producer_SHA256=%s", before["_uuid"], before["_version"], after["_version"], before["up"], after["up"], current)
	}
}

func nicStartTestSnapshot(t *testing.T, raw ovsdbClient.Client) ovsdb.Row {
	t.Helper()
	columns := []string{"_uuid", "_version"}
	for name := range ovnNB.Schema().Tables["Logical_Switch_Port"].Columns {
		columns = append(columns, name)
	}

	result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(nicStartPort)}}, Columns: columns})
	require.Len(t, result[0].Rows, 1)
	return result[0].Rows[0]
}

func nicStartTestPublish(nb *NB, owner NICPrefixOwner) error {
	return nb.PublishNICPrefixes(context.Background(), "copy_start", []net.IPNet{{IP: net.ParseIP("192.0.2.2"), Mask: net.CIDRMask(32, 32)}}, "", "", nil, owner)
}

// Every actual LSP producer column and original identity is protected both before and inside the
// transaction. Only northd's up (and its resulting _version rewrite) is tolerated.
func TestNICFreshStartPublicationRaces(t *testing.T) {
	changes := []string{"none", "up", "addresses", "dynamic_addresses", "enabled", "external_ids", "name", "options", "parent_name", "port_security", "tag", "tag_request", "type", "dhcpv4_options", "dhcpv6_options", "ha_chassis_group", "mirror_rules", "source", "generation", "producer", "replacement", "parent", "root"}
	for _, stage := range []string{"capture", "before-publication", "publication", "republication"} {
		for _, change := range changes {
			t.Run(stage+"/"+change, func(t *testing.T) {
				nb, raw, recorder := nicStartTestFixture(t)
				var owner NICPrefixOwner
				var err error
				if stage != "capture" {
					owner, err = nb.NewNICStartPrefixOwner(context.Background(), nicStartSwitch, nicStartPort, nicStartSource)
					require.NoError(t, err)
				}

				if stage == "republication" {
					require.NoError(t, nicStartTestPublish(nb, owner))
				}

				raced := change == "none"
				if stage == "before-publication" {
					nicStartTestChange(t, raw, change)
					raced = true
				} else {
					recorder.before = func(ops []ovsdb.Operation) {
						if raced {
							return
						}

						for _, op := range ops {
							if op.Op == ovsdb.OperationWait && op.Table == "Logical_Switch_Port" {
								raced = true
								nicStartTestChange(t, raw, change)
								return
							}
						}
					}
				}

				if stage == "capture" {
					_, err = nb.NewNICStartPrefixOwner(context.Background(), nicStartSwitch, nicStartPort, nicStartSource)
				} else {
					err = nicStartTestPublish(nb, owner)
				}

				if change == "none" || change == "up" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}

				require.True(t, raced)
				// A refused publication leaves shared fields unchanged (or preserves the already
				// published contribution when the race happened during an idempotent retry).
				result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "copy_start_ip4"}}, Columns: []string{"addresses"}})
				values, readErr := nicCleanupStringSet(result[0].Rows[0]["addresses"])
				require.NoError(t, readErr)
				if stage == "republication" || stage != "capture" && (change == "none" || change == "up") {
					require.Equal(t, []string{"192.0.2.2/32"}, values)
				} else {
					require.Empty(t, values)
				}
			})
		}
	}
}

func TestNICStartOldOwnerVersionGuard(t *testing.T) {
	nb, raw, _ := nicStartTestFixture(t)
	owner, err := nb.NewNICPrefixOwner(context.Background(), nicStartSwitch, nicStartPort, nicStartSource)
	require.NoError(t, err)
	require.Empty(t, owner.StartContent)
	encoded, err := json.Marshal(owner)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "StartContent")
	var restored NICPrefixOwner
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, owner, restored)
	nicStartTestChange(t, raw, "up")
	require.Error(t, nicStartTestPublish(nb, restored))

	// An incomplete optional guard is rejected rather than falling back to weaker capture.
	for _, invalid := range []string{"incomplete", strings.Repeat("z", 64)} {
		restored.StartContent = invalid
		require.Error(t, restored.Validate(nb.BackendID(), restored.port()))
	}
}

func TestNICStartActualSchema(t *testing.T) {
	path := os.Getenv("INCUS_TEST_OVN_NB_SCHEMA")
	if path == "" {
		t.Skip("Actual deployment schema not supplied")
	}

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var actual ovsdb.DatabaseSchema
	require.NoError(t, json.Unmarshal(data, &actual))
	// Decode both schema forms through libovsdb: the vendor file uses atomic-type shorthand,
	// while model.Schema emits the equivalent expanded representation.
	expected, err := json.Marshal(ovnNB.Schema())
	require.NoError(t, err)
	var model ovsdb.DatabaseSchema
	require.NoError(t, json.Unmarshal(expected, &model))
	require.Equal(t, model, actual)
	t.Logf("Native fixture matches actual deployment schema %s SHA256=%x", actual.Version, sha256.Sum256(data))
}

func TestNICStartOwnerCleanupRoundTrip(t *testing.T) {
	nb, raw, _ := nicStartTestFixture(t)
	owner, err := nb.NewNICStartPrefixOwner(context.Background(), nicStartSwitch, nicStartPort, nicStartSource)
	require.NoError(t, err)
	require.Len(t, owner.StartContent, sha256.Size*2)
	require.NoError(t, nicStartTestPublish(nb, owner))
	nicStartTestChange(t, raw, "up")
	port, err := nb.CaptureNICPortCleanup(context.Background(), nicStartSwitch, nicStartPort, nicStartSource)
	require.NoError(t, err)
	captured, err := nb.CaptureNICPrefixOwner(context.Background(), port)
	require.NoError(t, err)
	require.Equal(t, owner, captured)
	addresses := []net.IPNet{{IP: net.ParseIP("192.0.2.2"), Mask: net.CIDRMask(32, 32)}}
	plan, err := nb.CaptureNICAddressSetCleanup(context.Background(), "copy_start", addresses, captured)
	require.NoError(t, err)
	require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "copy_start", addresses))
	result := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "copy_start_ip4"}}, Columns: []string{"addresses"}})
	values, err := nicCleanupStringSet(result[0].Rows[0]["addresses"])
	require.NoError(t, err)
	require.Empty(t, values)
}
