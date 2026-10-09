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

func nicMACCleanupFixture() (*SB, *nicRouteCleanupClient, string, string) {
	root, nbRoot, router, datapath := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	original, foreignPort, foreignDatapath := uuid.NewString(), uuid.NewString(), uuid.NewString()
	row := func(id string, dp string, port string) ovsdb.Row {
		return ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: id}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "datapath": ovsdb.UUID{GoUUID: dp}, "logical_port": port, "ip": "192.0.2.10", "mac": "00:16:3e:00:00:10"}
	}

	c := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"SB_Global":        {{"_uuid": ovsdb.UUID{GoUUID: root}}},
		"Datapath_Binding": {{"_uuid": ovsdb.UUID{GoUUID: datapath}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"logical-router": router}}}},
		"MAC_Binding":      {row(original, datapath, "original-router-port"), row(foreignPort, datapath, "foreign-router-port"), row(foreignDatapath, uuid.NewString(), "original-router-port")},
	}}
	return &SB{client: c, backendID: root}, c, nbRoot, router
}

func captureNICMACTest(t *testing.T, sb *SB, nbRoot string, router string) NICMACBindingCleanup {
	t.Helper()
	plan, err := sb.CaptureNICMACBindingCleanup(context.Background(), nbRoot, router, "original-router-port", net.ParseIP("192.0.2.10"))
	require.NoError(t, err)
	return plan
}

func TestNICMACCleanupStoredReplayAndForeignRows(t *testing.T) {
	sb, c, nbRoot, router := nicMACCleanupFixture()
	plan := captureNICMACTest(t, sb, nbRoot, router)
	require.Len(t, plan.Rows, 1)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICMACBindingCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &SB{client: c, backendID: sb.backendID}
	require.NoError(t, fresh.ApplyNICMACBindingCleanup(context.Background(), stored))
	require.Len(t, c.tables["MAC_Binding"], 2)
	require.NoError(t, fresh.ApplyNICMACBindingCleanup(context.Background(), stored))
	require.Len(t, c.tables["MAC_Binding"], 2)
	replacement := ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "datapath": ovsdb.UUID{GoUUID: plan.DatapathUUID}, "logical_port": string(plan.Port), "ip": plan.Rows[0].IP}
	c.tables["MAC_Binding"] = append(c.tables["MAC_Binding"], replacement)
	require.NoError(t, fresh.ApplyNICMACBindingCleanup(context.Background(), stored))
	require.Len(t, c.tables["MAC_Binding"], 3)
	require.Equal(t, replacement, c.tables["MAC_Binding"][2])
}

func TestNICMACCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"missing-root", "missing-router", "missing-datapath", "duplicate-datapath", "duplicate-cache", "bad-row-version", "invalid-ip", "read-error", "short-result"} {
		t.Run(name, func(t *testing.T) {
			sb, c, nbRoot, router := nicMACCleanupFixture()
			ip := net.ParseIP("192.0.2.10")
			switch name {
			case "missing-root":
				sb.backendID = ""
			case "missing-router":
				router = ""
			case "missing-datapath":
				c.tables["Datapath_Binding"] = nil
			case "duplicate-datapath":
				c.tables["Datapath_Binding"] = append(c.tables["Datapath_Binding"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"logical-router": router}}})
			case "duplicate-cache":
				duplicate := ovsdb.Row{}
				for key, value := range c.tables["MAC_Binding"][0] {
					duplicate[key] = value
				}

				duplicate["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				c.tables["MAC_Binding"] = append(c.tables["MAC_Binding"], duplicate)
			case "bad-row-version":
				c.tables["MAC_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: "bad"}
			case "invalid-ip":
				ip = nil
			case "read-error":
				c.failNext = errors.New("inert SB select failed")
			case "short-result":
				c.truncateNext = true
			}

			count := len(c.tables["MAC_Binding"])
			_, err := sb.CaptureNICMACBindingCleanup(context.Background(), nbRoot, router, "original-router-port", ip)
			require.Error(t, err)
			require.Len(t, c.tables["MAC_Binding"], count)
			for _, ops := range c.calls {
				for _, op := range ops {
					require.Contains(t, []string{ovsdb.OperationSelect, ovsdb.OperationWait}, op.Op)
				}
			}
		})
	}
}

func TestNICMACCleanupReplayIdentityRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "stale-client", "datapath-replaced", "datapath-retargeted", "future-plan", "malformed-row"} {
		t.Run(name, func(t *testing.T) {
			sb, c, nbRoot, router := nicMACCleanupFixture()
			plan := captureNICMACTest(t, sb, nbRoot, router)
			switch name {
			case "root-replaced":
				c.tables["SB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "stale-client":
				sb = &SB{client: c, backendID: uuid.NewString()}
			case "datapath-replaced":
				c.tables["Datapath_Binding"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "datapath-retargeted":
				c.tables["Datapath_Binding"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"logical-router": uuid.NewString()}}
			case "future-plan":
				plan.Version = 99
			case "malformed-row":
				plan.Rows[0].UUID = "bad"
			}

			require.Error(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
			require.Len(t, c.tables["MAC_Binding"], 3)
		})
	}
}

func TestNICMACCleanupRetainsRelearnedGeneration(t *testing.T) {
	sb, c, nbRoot, router := nicMACCleanupFixture()
	plan := captureNICMACTest(t, sb, nbRoot, router)
	// A relearn records another MAC; a version change alone is server-local (database restart).
	c.tables["MAC_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	c.tables["MAC_Binding"][0]["mac"] = "00:16:3e:00:00:99"
	require.NoError(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
	require.Len(t, c.tables["MAC_Binding"], 3)
	require.Equal(t, ovsdb.UUID{GoUUID: plan.Rows[0].UUID}, c.tables["MAC_Binding"][0]["_uuid"])
	// Success proves only the stored cache generation is obsolete, not fencing
	// or completion of BGP, host, NAT or the full durable cleanup attempt.
}

func TestNICMACCleanupGuardAtDispatch(t *testing.T) {
	for _, name := range []string{"relearn", "root", "datapath"} {
		t.Run(name, func(t *testing.T) {
			sb, c, nbRoot, router := nicMACCleanupFixture()
			plan := captureNICMACTest(t, sb, nbRoot, router)
			c.beforeWrite = func() {
				switch name {
				case "relearn":
					c.tables["MAC_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
					c.tables["MAC_Binding"][0]["mac"] = "00:16:3e:00:00:99"
				case "root":
					c.tables["SB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "datapath":
					c.tables["Datapath_Binding"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"logical-router": uuid.NewString()}}
				}
			}

			require.Error(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
			require.Len(t, c.tables["MAC_Binding"], 3)
			if name == "relearn" {
				require.NoError(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
				require.Len(t, c.tables["MAC_Binding"], 3)
			}
		})
	}
}

// Row versions are server-local: the leader executing the write sees other versions.
func TestNICMACCleanupServerLocalVersions(t *testing.T) {
	sb, c, nbRoot, router := nicMACCleanupFixture()
	plan := captureNICMACTest(t, sb, nbRoot, router)
	c.beforeWrite = func() {
		c.tables["MAC_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
		c.tables["Datapath_Binding"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	}

	require.NoError(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
	require.Len(t, c.tables["MAC_Binding"], 2)
}

// A database restart changes every server-local version before the stored plan is applied.
func TestNICMACCleanupAfterDatabaseRestart(t *testing.T) {
	sb, c, nbRoot, router := nicMACCleanupFixture()
	plan := captureNICMACTest(t, sb, nbRoot, router)
	for _, table := range []string{"MAC_Binding", "Datapath_Binding"} {
		for _, row := range c.tables[table] {
			row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
		}
	}

	require.NoError(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
	require.Len(t, c.tables["MAC_Binding"], 2)
}

func TestNICMACCleanupMissingReplyAndStoredRetry(t *testing.T) {
	sb, c, nbRoot, router := nicMACCleanupFixture()
	plan := captureNICMACTest(t, sb, nbRoot, router)
	c.afterWriteError = errors.New("inert committed cache invalidation missing reply")
	require.ErrorContains(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan), "missing reply")
	require.Len(t, c.tables["MAC_Binding"], 2)
	require.NoError(t, sb.ApplyNICMACBindingCleanup(context.Background(), plan))
	require.Len(t, c.tables["MAC_Binding"], 2)
}

func TestNICMACCleanupAbsentCacheAndCanceledRead(t *testing.T) {
	for _, name := range []string{"absent-cache", "canceled"} {
		t.Run(name, func(t *testing.T) {
			sb, c, nbRoot, router := nicMACCleanupFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "canceled" {
				cancel()
			} else {
				c.tables["MAC_Binding"] = nil
			}

			plan, err := sb.CaptureNICMACBindingCleanup(ctx, nbRoot, router, "original-router-port", net.ParseIP("192.0.2.10"))
			if name == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, c.calls)
				return
			}

			require.NoError(t, err)
			require.Empty(t, plan.Rows)
			require.NoError(t, sb.ApplyNICMACBindingCleanup(ctx, plan))
			require.Empty(t, c.tables["MAC_Binding"])
		})
	}
}
