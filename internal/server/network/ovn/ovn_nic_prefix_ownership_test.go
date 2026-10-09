package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovnNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
)

// Schema fills ordinary defaults omitted by the inert fixture's stored rows.
func (c *nicRouteCleanupClient) Schema() ovsdb.DatabaseSchema {
	schema := ovnNB.Schema()
	for table, rows := range c.tables {
		for _, row := range rows {
			for name, column := range schema.Tables[table].Columns {
				_, exists := row[name]
				if exists {
					continue
				}

				kind := column.Type
				if kind == ovsdb.TypeEnum {
					kind = column.TypeObj.Key.Type
				}

				switch kind {
				case ovsdb.TypeString:
					row[name] = ""
				case ovsdb.TypeBoolean:
					row[name] = false
				case ovsdb.TypeInteger, ovsdb.TypeReal:
					row[name] = 0
				case ovsdb.TypeUUID:
					row[name] = ovsdb.UUID{GoUUID: "00000000-0000-0000-0000-000000000000"}
				case ovsdb.TypeSet:
					row[name] = ovsdb.OvsSet{GoSet: []any{}}
				case ovsdb.TypeMap:
					row[name] = ovsdb.OvsMap{GoMap: map[any]any{}}
				}
			}
		}
	}

	return schema
}

func nicPrefixTestOwner(t *testing.T, nb *NB, client *nicRouteCleanupClient, name string) NICPrefixOwner {
	t.Helper()
	sw, port := uuid.NewString(), uuid.NewString()
	client.tables["Logical_Switch"] = append(client.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: sw}, "name": name + "-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}})
	client.tables["Logical_Switch_Port"] = append(client.tables["Logical_Switch_Port"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: port}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": name, "enabled": true, "type": "", "options": nicCleanupStringMapWire(map[string]string{}), "external_ids": nicCleanupStringMapWire(map[string]string{ovnExtIDIncusLocation: "source-member"})})
	owner, err := nb.NewNICPrefixOwner(context.Background(), OVNSwitch(name+"-switch"), OVNSwitchPort(name), "source-member")
	require.NoError(t, err)
	return owner
}

func nicPrefixTestCapturedOwner(t *testing.T, nb *NB) NICPrefixOwner {
	t.Helper()
	port, err := nb.CaptureNICPortCleanup(context.Background(), "nic-switch", "nic", "source-member")
	require.NoError(t, err)
	owner, err := nb.CaptureNICPrefixOwner(context.Background(), port)
	require.NoError(t, err)
	return owner
}

func nicPrefixTestPublish(t *testing.T, nb *NB, client *nicRouteCleanupClient, addresses []net.IPNet, sw OVNSwitch, port OVNSwitchPort) NICPrefixOwner {
	t.Helper()
	if len(client.tables["Address_Set"]) == 0 {
		for _, family := range []int{4, 6} {
			client.tables["Address_Set"] = append(client.tables["Address_Set"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": fmt.Sprintf("original_ip%d", family), "addresses": nicCleanupStringSetWire(nil), "external_ids": nicCleanupStringMapWire(map[string]string{"foreign": "untouched"})})
		}
	}

	owner := nicPrefixTestOwner(t, nb, client, "nic")
	require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, sw, port, addresses, owner))
	return owner
}

func TestNICPrefixPublicationRetainedOwnersAndReplay(t *testing.T) {
	for _, baseline := range []bool{false, true} {
		t.Run(fmt.Sprintf("baseline-%t", baseline), func(t *testing.T) {
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			first := nicPrefixTestCapturedOwner(t, nb)
			if baseline {
				require.NoError(t, nb.UpdateAddressSetAdd(context.Background(), "original", addresses...))
				require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(context.Background(), "original-proxy", addresses, nil))
			}

			second := nicPrefixTestOwner(t, nb, client, "sibling")
			_, overlap, _ := net.ParseCIDR("198.51.100.0/25")
			sibling := append(append([]net.IPNet{}, addresses...), *overlap)
			require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", sibling, "original-switch", "original-proxy", sibling, second))
			addressPlan, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, first)
			require.NoError(t, err)
			proxyPlan, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, first)
			require.NoError(t, err)
			wire, err := json.Marshal(struct {
				Address NICAddressSetCleanup
				Proxy   NICARPProxyCleanup
			}{addressPlan, proxyPlan})
			require.NoError(t, err)
			var stored struct {
				Address NICAddressSetCleanup
				Proxy   NICARPProxyCleanup
			}

			require.NoError(t, json.Unmarshal(wire, &stored))
			fresh := &NB{client: client, backendID: nb.backendID}
			client.afterWriteError = errors.New("lost committed release reply")
			require.Error(t, fresh.ApplyNICAddressSetCleanup(context.Background(), stored.Address, "original", addresses))
			require.NoError(t, fresh.ApplyNICAddressSetCleanup(context.Background(), stored.Address, "original", addresses))
			require.NoError(t, fresh.ApplyNICARPProxyCleanup(context.Background(), stored.Proxy, "original-proxy", addresses))
			values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
			require.NoError(t, err)
			require.Contains(t, values, addresses[0].String())
			require.Contains(t, values, overlap.String())
			ap, err := fresh.CaptureNICAddressSetCleanup(context.Background(), "original", sibling, second)
			require.NoError(t, err)
			pp, err := fresh.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", sibling, second)
			require.NoError(t, err)
			require.NoError(t, fresh.ApplyNICAddressSetCleanup(context.Background(), ap, "original", sibling))
			require.NoError(t, fresh.ApplyNICARPProxyCleanup(context.Background(), pp, "original-proxy", sibling))
			values, err = nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
			require.NoError(t, err)
			require.Equal(t, baseline, containsPrefix(values, addresses[0].String()))
			require.NotContains(t, values, overlap.String())
			options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
			require.NoError(t, err)
			require.Equal(t, baseline, containsPrefix(stringsFields(options["arp_proxy"]), addresses[0].String()))
			require.Contains(t, options["arp_proxy"], "203.0.113.0/24")
		})
	}
}

func containsPrefix(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func stringsFields(value string) []string { return strings.Fields(value) }

func TestNICPrefixReplacementUncertainPublicationAndRollback(t *testing.T) {
	for _, outcome := range []string{"reply-lost", "dispatch-refused", "published"} {
		t.Run(outcome, func(t *testing.T) {
			nb, client, original := nicARPProxyCleanupFixture(t)
			prior := nicPrefixTestCapturedOwner(t, nb)
			owner, err := nb.NewNICPrefixOwner(context.Background(), "nic-switch", "nic", "source-member")
			require.NoError(t, err)
			_, replacement, _ := net.ParseCIDR("192.0.2.0/24")
			requested := []net.IPNet{*replacement}
			switch outcome {
			case "reply-lost":
				client.afterWriteError = errors.New("lost committed replacement reply")
			case "dispatch-refused":
				client.beforeWrite = func() {
					external, err := nicCleanupStringMap(client.tables["Address_Set"][1]["external_ids"])
					require.NoError(t, err)
					external["foreign-conflict"] = "kept"
					client.tables["Address_Set"][1]["external_ids"] = nicCleanupStringMapWire(external)
				}
			}

			err = nb.PublishNICPrefixes(context.Background(), "original", requested, "original-switch", "original-proxy", requested, owner)
			if outcome == "published" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			if outcome == "reply-lost" {
				// Retry the exact allocation, even though the source port version changed on commit.
				require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", requested, "original-switch", "original-proxy", requested, owner))
			}

			if outcome != "dispatch-refused" {
				values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
				require.NoError(t, err)
				require.Contains(t, values, replacement.String())
				require.NotContains(t, values, original[0].String())
				client.afterWriteError = errors.New("lost committed rollback reply")
				require.Error(t, nb.RollbackNICPrefixes(context.Background(), "original", requested, "original-switch", "original-proxy", requested, owner))
			}

			require.NoError(t, nb.RollbackNICPrefixes(context.Background(), "original", requested, "original-switch", "original-proxy", requested, owner))
			require.Equal(t, prior, nicPrefixTestCapturedOwner(t, nb))
			if outcome == "dispatch-refused" {
				external, err := nicCleanupStringMap(client.tables["Address_Set"][1]["external_ids"])
				require.NoError(t, err)
				require.Equal(t, "kept", external["foreign-conflict"])
			}

			values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
			require.NoError(t, err)
			require.Contains(t, values, original[0].String())
			require.NotContains(t, values, replacement.String())
			options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
			require.NoError(t, err)
			require.Contains(t, options["arp_proxy"], original[0].String())
			require.NotContains(t, options["arp_proxy"], replacement.String())
			// Original ownership remains usable by the same capture/removal path as Stop.
			ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", original, prior)
			require.NoError(t, err)
			require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", original))
		})
	}
}

func TestNICPrefixInitialPublicationAtomicFailures(t *testing.T) {
	for _, outcome := range []string{"reply-lost", "late-proxy", "before-dispatch"} {
		t.Run(outcome, func(t *testing.T) {
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			owner := nicPrefixTestOwner(t, nb, client, "new-nic")
			before := nicARPProxyTables(client.tables)
			switch outcome {
			case "reply-lost":
				client.afterWriteError = errors.New("lost publication reply")
			case "late-proxy":
				client.beforeWrite = func() {
					options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
					require.NoError(t, err)
					options["foreign-conflict"] = "kept"
					client.tables["Logical_Switch_Port"][0]["options"] = nicCleanupStringMapWire(options)
				}

			case "before-dispatch":
				client.failNext = errors.New("failed read before publication")
			}

			require.Error(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "original-switch", "original-proxy", addresses, owner))
			if outcome == "before-dispatch" {
				require.Equal(t, before, nicARPProxyTables(client.tables))
			}

			require.NoError(t, nb.RollbackNICPrefixes(context.Background(), "original", addresses, "original-switch", "original-proxy", addresses, owner))
			if outcome == "late-proxy" {
				options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
				require.NoError(t, err)
				require.Equal(t, "kept", options["foreign-conflict"])
			}
			// The initial NIC remains the last retained owner after the failed new Start.
			ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, nicPrefixTestCapturedOwner(t, nb))
			require.NoError(t, err)
			require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
			values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
			require.NoError(t, err)
			require.NotContains(t, values, addresses[0].String())
		})
	}
}

func TestNICPrefixWritersPreserveNICAndBaseline(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	owner := nicPrefixTestCapturedOwner(t, nb)
	require.NoError(t, nb.EnsureAddressSetPrefixes(context.Background(), "original", addresses...))
	require.NoError(t, nb.UpdateAddressSetRemove(context.Background(), "original", addresses...))
	values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	require.Contains(t, values, addresses[0].String())
	// Generic add creates an independent baseline contributor, even for an equal owned prefix.
	require.NoError(t, nb.UpdateAddressSetAdd(context.Background(), "original", addresses...))
	require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(context.Background(), "original-proxy", addresses, nil))
	pp, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, owner)
	require.NoError(t, err)
	require.NoError(t, nb.ClearLogicalSwitchPortARPProxy(context.Background(), "original-proxy"))
	// Clear withdraws every proxy entry and retires NIC owners; immutable Stop replay consumes the same receipt.
	require.NoError(t, nb.ApplyNICARPProxyCleanup(context.Background(), pp, "original-proxy", addresses))
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.NotContains(t, options, "arp_proxy")
	require.Equal(t, "original-router", options["router-port"])
	ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
	require.NoError(t, err)
	require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
	values, err = nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	require.Contains(t, values, addresses[0].String())
}

func TestNICPrefixUnknownProvenanceAndPartialRows(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	owner := nicPrefixTestCapturedOwner(t, nb)
	ids, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["external_ids"])
	require.NoError(t, err)
	delete(ids, nicPrefixMetadata)
	client.tables["Logical_Switch_Port"][0]["external_ids"] = nicCleanupStringMapWire(ids)
	before := nicARPProxyTables(client.tables)
	_, err = nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, owner)
	require.Error(t, err)
	require.Equal(t, before, nicARPProxyTables(client.tables))
	// Without any ownership record the routed-mode clear removes the whole option, as before.
	require.NoError(t, nb.ClearLogicalSwitchPortARPProxy(context.Background(), "original-proxy"))
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.NotContains(t, options, "arp_proxy")
	require.Equal(t, "retained", options["foreign"])
	client.tables["Address_Set"] = client.tables["Address_Set"][1:]
	before = nicARPProxyTables(client.tables)
	require.Error(t, nb.UpdateAddressSetAdd(context.Background(), "original", addresses...))
	require.Error(t, nb.EnsureAddressSetPrefixes(context.Background(), "original", addresses...))
	require.Equal(t, before, nicARPProxyTables(client.tables))
}

func TestNICPrefixPreexistingEqualBaselinePreserved(t *testing.T) {
	for _, mode := range []string{"tracked-nic", "upstream-nic"} {
		t.Run(mode, func(t *testing.T) {
			nicPrefixPreexistingEqualBaseline(t, mode == "upstream-nic")
		})
	}
}

// nicPrefixPreexistingEqualBaseline covers equal entries that predate ownership tracking: a tracked
// NIC keeps another contributor's entry, while an upstream-created NIC's own entries are released.
func nicPrefixPreexistingEqualBaseline(t *testing.T, upstream bool) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	// The fixture represents legacy effects with no ownership marker or row ledger.
	for _, table := range []string{"Address_Set", "Logical_Switch_Port"} {
		for _, row := range client.tables[table] {
			ids, err := nicCleanupStringMap(row["external_ids"])
			require.NoError(t, err)
			delete(ids, nicPrefixMetadata)
			delete(ids, nicPrefixGeneration)
			if row["name"] == "nic" && !upstream {
				ids[nicConfigPublicationKey] = nicPrefixEncode(NICConfigPublication{Source: "source-member"})
			}

			row["external_ids"] = nicCleanupStringMapWire(ids)
		}
	}

	owner, err := nb.NewNICPrefixOwner(context.Background(), "nic-switch", "nic", "source-member")
	require.NoError(t, err)
	require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "original-switch", "original-proxy", addresses, owner))
	ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
	require.NoError(t, err)
	pp, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, owner)
	require.NoError(t, err)
	require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
	require.NoError(t, nb.ApplyNICARPProxyCleanup(context.Background(), pp, "original-proxy", addresses))
	// A tracked NIC's cleanup keeps the equal entry that predates ownership tracking; an
	// upstream-created NIC's own entries are released as upstream did.
	values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	if upstream {
		require.NotContains(t, values, addresses[0].String())
		require.NotContains(t, options["arp_proxy"], addresses[0].String())
		require.Contains(t, options["arp_proxy"], "203.0.113.0/24")
		return
	}

	require.Contains(t, values, addresses[0].String())
	require.Contains(t, options["arp_proxy"], addresses[0].String())
	// The network's own removal withdraws it, as an untracked row would.
	require.NoError(t, nb.UpdateAddressSetRemove(context.Background(), "original", addresses...))
	require.NoError(t, nb.UpdateLogicalSwitchPortARPProxy(context.Background(), "original-proxy", nil, addresses))
	values, err = nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	require.NotContains(t, values, addresses[0].String())
	options, err = nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.NotContains(t, options["arp_proxy"], addresses[0].String())
	require.Contains(t, options["arp_proxy"], "203.0.113.0/24")
}

// TestAddressSetUntrackedRemoval covers a user address set: removals are applied in OVN and the
// row never gains NIC ownership metadata.
func TestAddressSetUntrackedRemoval(t *testing.T) {
	nb, client, _ := nicARPProxyCleanupFixture(t)
	_, one, _ := net.ParseCIDR("192.0.2.1/32")
	_, two, _ := net.ParseCIDR("192.0.2.2/32")
	for _, family := range []int{4, 6} {
		client.tables["Address_Set"] = append(client.tables["Address_Set"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": fmt.Sprintf("incus_set7_ip%d", family), "addresses": nicCleanupStringSetWire(nil), "external_ids": nicCleanupStringMapWire(map[string]string{})})
	}

	set := func() ovsdb.Row {
		for _, row := range client.tables["Address_Set"] {
			if row["name"] == "incus_set7_ip4" {
				return row
			}
		}

		return nil
	}

	set()["addresses"] = nicCleanupStringSetWire([]string{one.String(), two.String()})
	handled, err := nb.nicPrefixUnowned(context.Background(), "Address_Set", []string{"incus_set7_ip4", "incus_set7_ip6"}, nil, []net.IPNet{*two}, false)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, nicPrefixLedgerValue(set()))
}

func TestNICPrefixPartialStoppedReplay(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	owner := nicPrefixTestCapturedOwner(t, nb)
	ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
	require.NoError(t, err)
	pp, err := nb.CaptureNICARPProxyCleanup(context.Background(), "original-switch", "original-proxy", addresses, owner)
	require.NoError(t, err)
	wire, err := json.Marshal(struct {
		Address NICAddressSetCleanup
		Proxy   NICARPProxyCleanup
	}{ap, pp})
	require.NoError(t, err)
	require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
	client.failNext = errors.New("proxy cleanup failed after address-set release")
	require.Error(t, nb.ApplyNICARPProxyCleanup(context.Background(), pp, "original-proxy", addresses))
	var stored struct {
		Address NICAddressSetCleanup
		Proxy   NICARPProxyCleanup
	}

	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &NB{client: client, backendID: nb.backendID}
	require.NoError(t, fresh.ApplyNICAddressSetCleanup(context.Background(), stored.Address, "original", addresses))
	require.NoError(t, fresh.ApplyNICARPProxyCleanup(context.Background(), stored.Proxy, "original-proxy", addresses))
	values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
	require.NoError(t, err)
	require.NotContains(t, values, addresses[0].String())
	options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
	require.NoError(t, err)
	require.NotContains(t, options["arp_proxy"], addresses[0].String())
	require.Contains(t, options["arp_proxy"], "203.0.113.0/24")
}

func TestNICPrefixChangedContributorRefused(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	owner := nicPrefixTestCapturedOwner(t, nb)
	ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
	require.NoError(t, err)
	row := client.tables["Address_Set"][0]
	ledger, ids, err := nb.nicPrefixLedger("Address_Set", row, false)
	require.NoError(t, err)
	contribution := ledger.Owners[owner.Generation]
	contribution.Owner.PortUUID = uuid.NewString()
	ledger.Owners[owner.Generation] = contribution
	ids[nicPrefixMetadata] = nicPrefixEncode(ledger)
	row["external_ids"] = nicCleanupStringMapWire(ids)
	before := nicARPProxyTables(client.tables)
	require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
	require.Equal(t, before, nicARPProxyTables(client.tables))
}

// TestNICPrefixColdMoveTransferredMarker covers a cold move: the previous member's allocation marker
// is accepted only once the port's producer publication was transferred to the new owner.
func TestNICPrefixColdMoveTransferredMarker(t *testing.T) {
	for _, transferred := range []bool{true, false} {
		t.Run(fmt.Sprintf("transferred-%t", transferred), func(t *testing.T) {
			ctx := context.Background()
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			var row ovsdb.Row
			for _, candidate := range client.tables["Logical_Switch_Port"] {
				if candidate["name"] == "nic" {
					row = candidate
				}
			}

			require.NotNil(t, row)
			ids, err := nicCleanupStringMap(row["external_ids"])
			require.NoError(t, err)
			require.NotEmpty(t, ids[nicPrefixGeneration])
			publisher := "source-member"
			if transferred {
				publisher = "target-member"
			}

			ids[ovnExtIDIncusLocation] = "target-member"
			ids[nicConfigPublicationKey] = nicPrefixEncode(NICConfigPublication{Source: publisher})
			row["external_ids"] = nicCleanupStringMapWire(ids)
			owner, err := nb.NewNICPrefixOwner(ctx, "nic-switch", "nic", "target-member")
			require.NoError(t, err)
			err = nb.PublishNICPrefixes(ctx, "original", addresses, "original-switch", "original-proxy", addresses, owner)
			if transferred {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, "previous allocation marker is invalid")
		})
	}
}

// TestNICPrefixCaptureTransferredMarker covers cleanup capture on the new member after a transfer.
func TestNICPrefixCaptureTransferredMarker(t *testing.T) {
	for _, transferred := range []bool{true, false} {
		t.Run(fmt.Sprintf("transferred-%t", transferred), func(t *testing.T) {
			ctx := context.Background()
			nb, client, _ := nicARPProxyCleanupFixture(t)
			var row ovsdb.Row
			for _, candidate := range client.tables["Logical_Switch_Port"] {
				if candidate["name"] == "nic" {
					row = candidate
				}
			}

			ids, err := nicCleanupStringMap(row["external_ids"])
			require.NoError(t, err)
			publisher := "source-member"
			if transferred {
				publisher = "target-member"
			}

			ids[ovnExtIDIncusLocation] = "target-member"
			ids[nicConfigPublicationKey] = nicPrefixEncode(NICConfigPublication{Source: publisher})
			row["external_ids"] = nicCleanupStringMapWire(ids)
			port, err := nb.CaptureNICPortCleanup(ctx, "nic-switch", "nic", "target-member")
			require.NoError(t, err)
			owner, err := nb.CaptureNICPrefixOwner(ctx, port)
			if transferred {
				require.NoError(t, err)
				require.Equal(t, "source-member", owner.Source)
				return
			}

			require.Error(t, err)
		})
	}
}

// TestNICPrefixLegacyAdoption covers Stop of an upstream-created port: its existing shared prefixes
// are adopted from the unowned baseline and then released, while unrelated baseline entries stay.
func TestNICPrefixLegacyAdoption(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy-publication", "recorded-publication"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nb, client, addresses := nicARPProxyCleanupFixture(t)
			for _, table := range []string{"Address_Set", "Logical_Switch_Port"} {
				for _, row := range client.tables[table] {
					ids, err := nicCleanupStringMap(row["external_ids"])
					require.NoError(t, err)
					delete(ids, nicPrefixMetadata)
					delete(ids, nicPrefixGeneration)
					switch {
					case row["name"] != "nic":
					case mode == "legacy-publication":
						ids[nicConfigPublicationKey] = nicPrefixEncode(NICConfigPublication{Source: "source-member", Legacy: true})
					case mode == "recorded-publication":
						ids[nicConfigPublicationKey] = nicPrefixEncode(NICConfigPublication{Source: "source-member"})
					}

					row["external_ids"] = nicCleanupStringMapWire(ids)
				}
			}

			client.tables["Address_Set"][0]["addresses"] = nicCleanupStringSetWire([]string{"192.0.2.0/24", addresses[0].String()})
			port, err := nb.CaptureNICPortCleanup(ctx, "nic-switch", "nic", "source-member")
			require.NoError(t, err)
			_, _, err = nb.CaptureNICPrefixOwnerTransferred(ctx, port)
			if mode == "recorded-publication" {
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrNICPrefixLegacy)
				return
			}

			require.ErrorIs(t, err, ErrNICPrefixLegacy)
			owner, err := nb.AdoptLegacyNICPrefixes(ctx, port, "original", addresses, "original-switch", "original-proxy", addresses)
			require.NoError(t, err)
			require.True(t, owner.Legacy)
			captured, _, err := nb.CaptureNICPrefixOwnerTransferred(ctx, port)
			require.NoError(t, err)
			require.Equal(t, owner, captured)
			ap, err := nb.CaptureNICAddressSetCleanup(ctx, "original", addresses, owner)
			require.NoError(t, err)
			pp, err := nb.CaptureNICARPProxyCleanup(ctx, "original-switch", "original-proxy", addresses, owner)
			require.NoError(t, err)
			require.NoError(t, nb.ApplyNICAddressSetCleanup(ctx, ap, "original", addresses))
			require.NoError(t, nb.ApplyNICARPProxyCleanup(ctx, pp, "original-proxy", addresses))
			values, err := nicCleanupStringSet(client.tables["Address_Set"][0]["addresses"])
			require.NoError(t, err)
			require.Equal(t, []string{"192.0.2.0/24"}, values)
			options, err := nicCleanupStringMap(client.tables["Logical_Switch_Port"][0]["options"])
			require.NoError(t, err)
			require.Equal(t, "203.0.113.0/24", options["arp_proxy"])
		})
	}
}

// TestNICPrefixReceiptsPruned covers repeated start/stop cycles of one NIC: receipts of its completed
// earlier allocations and of retired ports are dropped instead of accumulating.
func TestNICPrefixReceiptsPruned(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	ledger := func() nicPrefixLedger {
		var l nicPrefixLedger
		require.NoError(t, json.Unmarshal([]byte(nicPrefixLedgerValue(client.tables["Address_Set"][0])), &l))
		return l
	}

	// A receipt of a port that no longer exists.
	l := ledger()
	gone := nicPrefixTestOwner(t, nb, client, "gone")
	gone.Generation = uuid.NewString()
	l.Released[gone.Generation] = nicPrefixContribution{Owner: gone, Prefixes: []string{}}
	ids, err := nicCleanupStringMap(client.tables["Address_Set"][0]["external_ids"])
	require.NoError(t, err)
	ids[nicPrefixMetadata] = nicPrefixEncode(l)
	client.tables["Address_Set"][0]["external_ids"] = nicCleanupStringMapWire(ids)
	client.tables["Logical_Switch_Port"] = slices.DeleteFunc(client.tables["Logical_Switch_Port"], func(row ovsdb.Row) bool { return row["name"] == "gone" })
	for range 3 {
		owner := nicPrefixTestCapturedOwner(t, nb)
		ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
		require.NoError(t, err)
		require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
		next, err := nb.NewNICPrefixOwner(context.Background(), "nic-switch", "nic", "source-member")
		require.NoError(t, err)
		require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "original-switch", "original-proxy", addresses, next))
	}

	// Only the receipt of the allocation the current one replaced remains, for its rollback.
	l = ledger()
	require.Len(t, l.Owners, 1)
	require.Len(t, l.Released, 1)
	for _, receipt := range l.Released {
		require.NotEqual(t, gone.PortUUID, receipt.Owner.PortUUID)
	}
}

// A live-migration source cleanup still proves its transfer with the target generation's receipt,
// however often the target republishes the port meanwhile.
func TestNICPrefixReceiptRetainedForMigrationSource(t *testing.T) {
	nb, client, addresses := nicARPProxyCleanupFixture(t)
	var transferred string
	for i := range 4 {
		owner := nicPrefixTestCapturedOwner(t, nb)
		if i == 0 {
			transferred = owner.Generation
		}

		ap, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", addresses, owner)
		require.NoError(t, err)
		require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), ap, "original", addresses))
		next, err := nb.NewNICPrefixOwner(context.Background(), "nic-switch", "nic", "source-member")
		require.NoError(t, err)
		require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "original-switch", "original-proxy", addresses, next, transferred))
	}

	var l nicPrefixLedger
	require.NoError(t, json.Unmarshal([]byte(nicPrefixLedgerValue(client.tables["Address_Set"][0])), &l))
	require.Contains(t, l.Released, transferred)
	require.Len(t, l.Released, 2, "the retained transfer receipt and the rollback predecessor")
}
