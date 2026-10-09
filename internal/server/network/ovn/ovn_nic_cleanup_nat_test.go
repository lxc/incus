package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

func nicNATCleanupFixture() (*NB, *nicRouteCleanupClient, NICNATCleanupTuple, string) {
	root, router, original, sibling := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tuple := NICNATCleanupTuple{Type: "snat", LogicalIP: "192.0.2.10/32", ExternalIP: "198.51.100.10"}
	row := func(id string, logical string, kind string) ovsdb.Row {
		return ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: id}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "type": kind, "logical_ip": logical, "external_ip": tuple.ExternalIP}
	}

	client := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"NB_Global":      {{"_uuid": ovsdb.UUID{GoUUID: root}}},
		"Logical_Router": {{"_uuid": ovsdb.UUID{GoUUID: router}, "name": "incus-net42-lr", "nat": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: original}, ovsdb.UUID{GoUUID: sibling}}}}},
		"NAT":            {row(original, tuple.LogicalIP, tuple.Type), row(sibling, "192.0.2.20/32", tuple.Type)},
	}}
	return &NB{client: client, backendID: root}, client, tuple, sibling
}

func captureNICNATTest(t *testing.T, nb *NB, tuple NICNATCleanupTuple) NICNATCleanup {
	t.Helper()
	plan, err := nb.CaptureNICNATCleanup(context.Background(), "incus-net42-lr", tuple)
	require.NoError(t, err)
	return plan
}

func TestNICNATCleanupStoredReplayAndForeignTranslation(t *testing.T) {
	nb, c, tuple, sibling := nicNATCleanupFixture()
	plan := captureNICNATTest(t, nb, tuple)
	require.Len(t, plan.Rows, 1)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICNATCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &NB{client: c, backendID: nb.backendID}
	require.NoError(t, fresh.ApplyNICNATCleanup(context.Background(), stored))
	require.Len(t, c.tables["NAT"], 1)
	require.Equal(t, ovsdb.UUID{GoUUID: sibling}, c.tables["NAT"][0]["_uuid"])
	ids, err := nicCleanupUUIDSet(c.tables["Logical_Router"][0]["nat"])
	require.NoError(t, err)
	require.Equal(t, []string{sibling}, ids)
	// A replacement with even the same complete tuple is not part of this attempt.
	replacement := uuid.NewString()
	c.tables["NAT"] = append(c.tables["NAT"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: replacement}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "type": tuple.Type, "logical_ip": tuple.LogicalIP, "external_ip": tuple.ExternalIP})
	c.tables["Logical_Router"][0]["nat"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: sibling}, ovsdb.UUID{GoUUID: replacement}}}
	require.NoError(t, fresh.ApplyNICNATCleanup(context.Background(), stored))
	require.Len(t, c.tables["NAT"], 2)
	require.Equal(t, ovsdb.UUID{GoUUID: replacement}, c.tables["NAT"][1]["_uuid"])
}

func TestNICNATCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"root-missing", "router-missing", "duplicate-matching", "shared-row", "read-error", "short-result", "invalid-family", "non-host-prefix"} {
		t.Run(name, func(t *testing.T) {
			nb, c, tuple, _ := nicNATCleanupFixture()
			switch name {
			case "root-missing":
				nb.backendID = ""
			case "router-missing":
				c.tables["Logical_Router"] = nil
			case "duplicate-matching":
				row := ovsdb.Row{}
				for key, value := range c.tables["NAT"][0] {
					row[key] = value
				}

				row["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				c.tables["NAT"] = append(c.tables["NAT"], row)
				parent := c.tables["Logical_Router"][0]
				set, ok := parent["nat"].(ovsdb.OvsSet)
				require.True(t, ok)
				set.GoSet = append(set.GoSet, row["_uuid"])
				parent["nat"] = set
			case "shared-row":
				c.tables["Logical_Router"] = append(c.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-router", "nat": ovsdb.OvsSet{GoSet: []any{c.tables["NAT"][0]["_uuid"]}}})
			case "read-error":
				c.failNext = errors.New("inert required NAT read failed")
			case "short-result":
				c.truncateNext = true
			case "invalid-family":
				tuple.ExternalIP = "2001:db8::1"
			case "non-host-prefix":
				tuple.LogicalIP = "192.0.2.0/24"
			}

			beforeCount := len(c.tables["NAT"])
			_, err := nb.CaptureNICNATCleanup(context.Background(), "incus-net42-lr", tuple)
			require.Error(t, err)
			require.Len(t, c.tables["NAT"], beforeCount)
			for _, ops := range c.calls {
				for _, op := range ops {
					require.Contains(t, []string{ovsdb.OperationSelect, ovsdb.OperationWait}, op.Op)
				}
			}
		})
	}
}

func TestNICNATCleanupReplayRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "stale-client", "router-replaced", "router-renamed", "logical-changed", "external-changed", "type-changed", "foreign-reference"} {
		t.Run(name, func(t *testing.T) {
			nb, c, tuple, _ := nicNATCleanupFixture()
			plan := captureNICNATTest(t, nb, tuple)
			switch name {
			case "root-replaced":
				c.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "stale-client":
				nb = &NB{client: c, backendID: uuid.NewString()}
			case "router-replaced":
				c.tables["Logical_Router"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "router-renamed":
				c.tables["Logical_Router"][0]["name"] = "foreign-router"
			case "logical-changed":
				c.tables["NAT"][0]["logical_ip"] = "192.0.2.30/32"
			case "external-changed":
				c.tables["NAT"][0]["external_ip"] = "198.51.100.30"
			case "type-changed":
				c.tables["NAT"][0]["type"] = "dnat"
			case "foreign-reference":
				c.tables["Logical_Router"] = append(c.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-router", "nat": ovsdb.OvsSet{GoSet: []any{c.tables["NAT"][0]["_uuid"]}}})
			}

			require.Error(t, nb.ApplyNICNATCleanup(context.Background(), plan))
			require.Len(t, c.tables["NAT"], 2)
		})
	}
}

func TestNICNATCleanupGuardAtDispatch(t *testing.T) {
	for _, name := range []string{"nat-generation", "foreign-parent", "root"} {
		t.Run(name, func(t *testing.T) {
			nb, c, tuple, _ := nicNATCleanupFixture()
			plan := captureNICNATTest(t, nb, tuple)
			c.beforeWrite = func() {
				switch name {
				case "nat-generation":
					c.tables["NAT"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
					c.tables["NAT"][0]["logical_ip"] = "198.51.100.99"
				case "foreign-parent":
					c.tables["Logical_Router"] = append(c.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-router", "nat": ovsdb.OvsSet{GoSet: []any{c.tables["NAT"][0]["_uuid"]}}})
				case "root":
					c.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				}
			}

			require.Error(t, nb.ApplyNICNATCleanup(context.Background(), plan))
			require.Len(t, c.tables["NAT"], 2)
		})
	}
}

func TestNICNATCleanupMissingReplyAndStoredRetry(t *testing.T) {
	nb, c, tuple, sibling := nicNATCleanupFixture()
	plan := captureNICNATTest(t, nb, tuple)
	c.afterWriteError = errors.New("inert committed NAT deletion missing reply")
	require.ErrorContains(t, nb.ApplyNICNATCleanup(context.Background(), plan), "missing reply")
	require.Len(t, c.tables["NAT"], 1)
	require.Equal(t, ovsdb.UUID{GoUUID: sibling}, c.tables["NAT"][0]["_uuid"])
	require.NoError(t, nb.ApplyNICNATCleanup(context.Background(), plan))
	require.Len(t, c.tables["NAT"], 1)
}

func TestNICNATCleanupNoMatchesAndLegacyTuple(t *testing.T) {
	for _, name := range []string{"foreign-logical", "foreign-type", "legacy-dnat-and-snat", "ipv6"} {
		t.Run(name, func(t *testing.T) {
			nb, c, tuple, _ := nicNATCleanupFixture()
			switch name {
			case "foreign-logical":
				tuple.LogicalIP = "192.0.2.99/32"
			case "foreign-type":
				c.tables["NAT"][0]["type"] = "dnat"
			case "legacy-dnat-and-snat":
				tuple.Type, tuple.LogicalIP = "dnat_and_snat", "192.0.2.10"
				c.tables["NAT"][0]["type"], c.tables["NAT"][0]["logical_ip"] = tuple.Type, tuple.LogicalIP
			case "ipv6":
				tuple.LogicalIP, tuple.ExternalIP = "2001:db8:1::10/128", "2001:db8:2::10"
				c.tables["NAT"][0]["logical_ip"], c.tables["NAT"][0]["external_ip"] = tuple.LogicalIP, tuple.ExternalIP
			}

			plan := captureNICNATTest(t, nb, tuple)
			wanted := 1
			if name == "foreign-logical" || name == "foreign-type" {
				wanted = 0
			}

			require.Len(t, plan.Rows, wanted)
			require.NoError(t, nb.ApplyNICNATCleanup(context.Background(), plan))
			require.Len(t, c.tables["NAT"], 2-wanted)
		})
	}
}

// TestNICNATCleanupServerRestart covers a stored plan applied after the database server restarted.
func TestNICNATCleanupServerRestart(t *testing.T) {
	nb, c, tuple, _ := nicNATCleanupFixture()
	plan := captureNICNATTest(t, nb, tuple)
	c.tables["NAT"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	require.NoError(t, nb.ApplyNICNATCleanup(context.Background(), plan))
	require.Len(t, c.tables["NAT"], 1)
}
