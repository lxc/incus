package ovn

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

func prefixPortableFixtureRow(t *testing.T, raw ovsdbClient.Client, table string) string {
	t.Helper()
	if table == "Address_Set" {
		results := referenceExec(t, raw,
			ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: ovsdb.Row{"name": "portable_ip4", "addresses": nicCleanupStringSetWire([]string{"198.51.100.0/24"}), "external_ids": nicCleanupStringMapWire(map[string]string{"foreign": "kept"})}},
			ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: ovsdb.Row{"name": "portable_ip6"}})
		return results[0].UUID.GoUUID
	}

	results := referenceExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, UUIDName: "prefix_port", Row: ovsdb.Row{"name": "portable-proxy", "type": "router", "options": nicCleanupStringMapWire(map[string]string{"router-port": "portable-router", "arp_proxy": "198.51.100.0/24", "foreign": "kept"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "portable-switch", "ports": nicPortSetForPrefix("prefix_port")}})
	return results[0].UUID.GoUUID
}

func TestNICPrefixPortableUnownedRealBackend(t *testing.T) {
	for _, relay := range []bool{false, true} {
		for _, table := range []string{"Address_Set", "Logical_Switch_Port"} {
			t.Run(fmt.Sprintf("relay-%t/%s", relay, table), func(t *testing.T) {
				nb, raw := referenceTestNB(t, relay)
				id := prefixPortableFixtureRow(t, raw, table)
				ctx := context.Background()
				var rows []ovsdb.Row
				var err error
				require.Eventually(t, func() bool {
					rows, err = nb.nicPrefixRead(ctx, table, "_uuid", ovsdb.UUID{GoUUID: id})
					return err == nil && len(rows) == 1
				}, 3*time.Second, 10*time.Millisecond)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Contains(t, rows[0], "_version")
				require.Contains(t, rows[0], "future_prefix_data")
				peer := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}})[0].Rows[0]
				if relay {
					require.NotEqual(t, peer["_version"], rows[0]["_version"])
				} else {
					require.Equal(t, peer["_version"], rows[0]["_version"])
				}

				_, address, err := net.ParseCIDR("203.0.113.0/24")
				require.NoError(t, err)
				if table == "Address_Set" {
					err = nb.UpdateAddressSetAdd(ctx, "portable", *address)
				} else {
					err = nb.UpdateLogicalSwitchPortARPProxy(ctx, "portable-proxy", []net.IPNet{*address}, nil)
				}

				require.NoError(t, err)
				current := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}})[0].Rows[0]
				ids, err := nicCleanupStringMap(current["external_ids"])
				require.NoError(t, err)
				if table == "Address_Set" {
					require.Equal(t, "kept", ids["foreign"])
					values, err := nicCleanupStringSet(current["addresses"])
					require.NoError(t, err)
					require.ElementsMatch(t, []string{"198.51.100.0/24", "203.0.113.0/24"}, values)
				} else {
					options, err := nicCleanupStringMap(current["options"])
					require.NoError(t, err)
					require.Equal(t, "kept", options["foreign"])
					require.ElementsMatch(t, []string{"198.51.100.0/24", "203.0.113.0/24"}, strings.Fields(options["arp_proxy"]))
				}
			})
		}
	}
}

func TestNICPrefixPortablePredicateRealBackend(t *testing.T) {
	for _, relay := range []bool{false, true} {
		for _, table := range []string{"Address_Set", "Logical_Switch_Port"} {
			changes := []string{"unchanged", "name", "metadata", "replace", "root", "generation", "future"}
			if table == "Address_Set" {
				changes = append(changes, "addresses")
			} else {
				changes = append(changes, "options", "type", "addresses", "enabled", "allocation-marker")
			}

			for _, change := range changes {
				t.Run(fmt.Sprintf("relay-%t/%s/%s", relay, table, change), func(t *testing.T) {
					nb, raw := referenceTestNB(t, relay)
					id := prefixPortableFixtureRow(t, raw, table)
					where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}
					ctx := context.Background()
					var rows []ovsdb.Row
					var err error
					require.Eventually(t, func() bool {
						rows, err = nb.nicPrefixRead(ctx, table, "_uuid", ovsdb.UUID{GoUUID: id})
						return err == nil && len(rows) == 1
					}, 3*time.Second, 10*time.Millisecond)
					require.NoError(t, err)
					require.Len(t, rows, 1)
					row := rows[0]
					ledger, external, err := nb.nicPrefixLedger(table, row, true)
					require.NoError(t, err)
					ledger.Managed = append(ledger.Managed, "203.0.113.0/24")
					ops := []ovsdb.Operation{nicCleanupRootWait(nb.BackendID()), nicPrefixRowWait(table, row), nicPrefixUpdate(table, row, ledger, external)}
					for _, op := range ops[1:] {
						columns := []string{}
						for _, condition := range op.Where {
							columns = append(columns, condition.Column)
						}

						require.Contains(t, columns, "_uuid")
						require.Contains(t, columns, "future_prefix_data")
						require.NotContains(t, columns, "_version")
					}

					update := ovsdb.Row{}
					switch change {
					case "name":
						update["name"] = "other-name"
					case "metadata", "allocation-marker":
						update["external_ids"] = nicCleanupStringMapWire(map[string]string{nicPrefixGeneration: uuid.NewString(), "foreign": "changed"})
					case "future":
						update["future_prefix_data"] = nicCleanupStringMapWire(map[string]string{"future": "changed"})
					case "addresses":
						update["addresses"] = nicCleanupStringSetWire([]string{"192.0.2.0/24"})
					case "options":
						update["options"] = nicCleanupStringMapWire(map[string]string{"router-port": "other-router", "arp_proxy": "192.0.2.0/24"})
					case "type":
						update["type"] = ""
					case "enabled":
						update["enabled"] = false
					case "replace":
						replacement := maps.Clone(row)
						delete(replacement, "_uuid")
						delete(replacement, "_version")
						if table == "Address_Set" {
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: where}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: replacement})
						} else {
							referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: where}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, Row: replacement, UUIDName: "replacement"}, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "portable-switch"}}, Row: ovsdb.Row{"ports": nicPortSetForPrefix("replacement")}})
						}

					case "root":
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: nb.BackendID()}}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{})}})
					case "generation":
						global := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "NB_Global"})[0].Rows[0]
						ids, err := nicCleanupStringMap(global["external_ids"])
						require.NoError(t, err)
						timeout, ok := nb.client.(*timeoutClient)
						require.True(t, ok)
						_, ok = timeout.Client.(*backendDB.FencedClient)
						require.True(t, ok)
						key := "incus:ovn-lifecycle:reference-fixture"
						require.NotEmpty(t, ids[key])
						require.NotEmpty(t, ids[key+":barrier"])
						ids[key] = uuid.NewString()
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: global["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
					}

					if len(update) > 0 {
						referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: table, Where: where, Row: update})
					}

					before := referenceContents(t, raw)
					err = nb.nicPrefixCommit(ctx, ops)
					if change == "unchanged" {
						require.NoError(t, err)
						current := referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: where})[0].Rows[0]
						ids, err := nicCleanupStringMap(current["external_ids"])
						require.NoError(t, err)
						require.NotEmpty(t, ids[nicPrefixMetadata])
					} else {
						require.Error(t, err)
						referenceSameContents(t, before, referenceContents(t, raw))
					}
				})
			}
		}
	}
}

type nicPrefixRecordedClient struct {
	ovsdbClient.Client
	t    *testing.T
	file *os.File
}

func (c *nicPrefixRecordedClient) Transact(ctx context.Context, ops ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	raw, err := json.Marshal(ops)
	require.NoError(c.t, err)
	_, err = c.file.Write(append(raw, '\n'))
	require.NoError(c.t, err)
	return c.Client.Transact(ctx, ops...)
}

func TestNICPrefixRealBackendLifecycle(t *testing.T) {
	for _, family := range []string{"ipv4", "ipv6", "dual", "empty"} {
		t.Run(family, func(t *testing.T) {
			nb, raw := referenceTestNB(t)
			evidence := os.Getenv("INCUS_NIC_PREFIX_EVIDENCE")
			if evidence == "" {
				evidence = t.TempDir()
			}

			path := filepath.Join(evidence, family+"-operations.jsonl")
			file, err := os.Create(path)
			require.NoError(t, err)
			defer file.Close()
			timeout, validTimeout := nb.client.(*timeoutClient)
			require.True(t, validTimeout)
			fenced, validFenced := timeout.Client.(*backendDB.FencedClient)
			require.True(t, validFenced)
			fenced.Client = &nicPrefixRecordedClient{Client: fenced.Client, t: t, file: file}
			referencePort(t, raw)
			referenceExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "original_ip4", "addresses": nicCleanupStringSetWire([]string{"203.0.113.0/24"}), "external_ids": nicCleanupStringMapWire(map[string]string{"foreign": "kept"})}},
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": "original_ip6", "addresses": nicCleanupStringSetWire([]string{"2001:db8:1::/64"}), "external_ids": nicCleanupStringMapWire(map[string]string{"foreign": "kept"})}})
			var addresses []net.IPNet
			for _, cidr := range []string{"198.51.100.0/24", "2001:db8:2::/64"} {
				_, prefix, err := net.ParseCIDR(cidr)
				require.NoError(t, err)
				if family == "dual" || family == "ipv4" && prefix.IP.To4() != nil || family == "ipv6" && prefix.IP.To4() == nil {
					addresses = append(addresses, *prefix)
				}
			}

			referenceExec(t, raw,
				ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "proxy", Row: ovsdb.Row{"name": "original-proxy", "type": "router", "addresses": nicCleanupStringSetWire([]string{"router"}), "options": nicCleanupStringMapWire(map[string]string{"router-port": "original-router-port", "arp_proxy": "203.0.113.0/24 2001:db8:1::/64", "foreign": "kept"})}},
				ovsdb.Operation{Op: ovsdb.OperationMutate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: "incus-net17-ls-int"}}, Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationInsert, Value: nicPortSetForPrefix("proxy")}}})
			owner, err := nb.NewNICPrefixOwner(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port", "source")
			require.NoError(t, err)
			require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "incus-net17-ls-int", "original-proxy", addresses, owner))
			candidates := append([]net.IPNet{}, addresses...)
			_, candidate, err := net.ParseCIDR("192.0.2.9/32")
			require.NoError(t, err)
			candidates = append(candidates, *candidate)
			plan, err := nb.CaptureNICAddressSetCleanup(context.Background(), "original", candidates, owner)
			require.NoError(t, err)
			captured, err := plan.CapturedPrefixes()
			require.NoError(t, err)
			require.NoError(t, plan.Validate(nb.BackendID(), "original", captured))
			proxy, err := nb.CaptureNICARPProxyCleanup(context.Background(), "incus-net17-ls-int", "original-proxy", candidates, owner)
			require.NoError(t, err)
			proxyPrefixes, err := proxy.CapturedPrefixes()
			require.NoError(t, err)
			require.Error(t, proxy.Validate(nb.BackendID(), "original-proxy", candidates))
			require.NoError(t, proxy.Validate(nb.BackendID(), "original-proxy", proxyPrefixes))
			require.NoError(t, nb.ApplyNICARPProxyCleanup(context.Background(), proxy, "original-proxy", proxyPrefixes))
			require.NoError(t, nb.ApplyNICARPProxyCleanup(context.Background(), proxy, "original-proxy", proxyPrefixes))
			proxyRows, err := nb.nicPrefixRead(context.Background(), "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: proxy.PortUUID})
			require.NoError(t, err)
			options, err := nicCleanupStringMap(proxyRows[0]["options"])
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"203.0.113.0/24", "2001:db8:1::/64"}, strings.Fields(options["arp_proxy"]))
			require.Equal(t, "kept", options["foreign"])

			require.Error(t, plan.Validate(nb.BackendID(), "original", candidates))
			require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", captured))
			require.NoError(t, nb.ApplyNICAddressSetCleanup(context.Background(), plan, "original", addresses))
			for i, row := range plan.Rows {
				actual, err := nb.nicPrefixRead(context.Background(), "Address_Set", "_uuid", ovsdb.UUID{GoUUID: row.UUID})
				require.NoError(t, err)
				values, err := nicCleanupStringSet(actual[0]["addresses"])
				require.NoError(t, err)
				require.Equal(t, []string{[]string{"203.0.113.0/24", "2001:db8:1::/64"}[i]}, values)
				ids, err := nicCleanupStringMap(actual[0]["external_ids"])
				require.NoError(t, err)
				require.Equal(t, "kept", ids["foreign"])
			}

			bad := plan
			bad.RootUUID = owner.PortUUID
			require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), bad, "original", captured))
			bad = plan
			bad.Rows = append([]NICAddressSetCleanupRow{}, plan.Rows...)
			bad.Rows[0].Ownership.Prefixes = []string{"2001:db8:2::/64"}
			require.Error(t, nb.ApplyNICAddressSetCleanup(context.Background(), bad, "original", captured))

			portUUID, err := nb.GetLogicalSwitchPortUUID(context.Background(), "incus-net17-instance-port")
			require.NoError(t, err)
			referenceExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "incus_net17"}})
			require.NoError(t, nb.UpdatePortGroupMembers(context.Background(), map[OVNPortGroup][]OVNSwitchPortUUID{"incus_net17": {portUUID}}, nil))
			require.NoError(t, nb.ClearPortGroupPortACLRules(context.Background(), "incus_net17", "incus-net17-instance-port"))
			require.NoError(t, nb.SetLogicalSwitchQoSRules(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port"))
			require.Error(t, nb.RollbackNICPrefixes(context.Background(), "original", addresses, "incus-net17-ls-int", "original-proxy", addresses, owner))
			owner, err = nb.NewNICPrefixOwner(context.Background(), "incus-net17-ls-int", "incus-net17-instance-port", "source")
			require.NoError(t, err)
			require.NoError(t, nb.PublishNICPrefixes(context.Background(), "original", addresses, "incus-net17-ls-int", "original-proxy", addresses, owner))
			require.NoError(t, nb.RollbackNICPrefixes(context.Background(), "original", addresses, "incus-net17-ls-int", "original-proxy", addresses, owner))
			require.NoError(t, nb.RollbackNICPrefixes(context.Background(), "original", addresses, "incus-net17-ls-int", "original-proxy", addresses, owner))
		})
	}
}

func nicPortSetForPrefix(id string) ovsdb.OvsSet {
	return ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: id}}}
}
