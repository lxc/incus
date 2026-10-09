package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

// This inert transaction client evaluates the constructed RFC7047 guards and atomic writes in memory.
// No NewNB, socket, server, cache monitor or operational client is constructed.
type nicRouteCleanupClient struct {
	ovsdbClient.Client
	tables          map[string][]ovsdb.Row
	calls           [][]ovsdb.Operation
	beforeWrite     func()
	failNext        error
	truncateNext    bool
	afterWriteError error
}

func nicRouteSetStrings(value any) any {
	{
		b, ok := value.(bool)
		if ok {
			return []string{fmt.Sprintf("%t", b)}
		}
	}

	{
		m, ok := value.(ovsdb.OvsMap)
		if ok {
			result := make(map[string]string, len(m.GoMap))
			for key, entry := range m.GoMap {
				k, keyOK := key.(string)
				v, valueOK := entry.(string)
				if !keyOK || !valueOK {
					return value
				}

				result[k] = v
			}

			return result
		}
	}

	// RFC7047 permits a singleton UUID set to be encoded as its scalar UUID.
	{
		id, ok := value.(ovsdb.UUID)
		if ok {
			return []string{id.GoUUID}
		}
	}

	set, ok := value.(ovsdb.OvsSet)
	if !ok {
		return value
	}

	values := make([]string, 0, len(set.GoSet))
	for _, entry := range set.GoSet {
		{
			id, ok := entry.(ovsdb.UUID)
			if ok {
				values = append(values, id.GoUUID)
			} else {
				b, ok := entry.(bool)
				if ok {
					values = append(values, fmt.Sprintf("%t", b))
				} else {
					values = append(values, entry.(string))
				}
			}
		}
	}

	slices.Sort(values)
	return values
}

func nicRouteRowMatches(row ovsdb.Row, where []ovsdb.Condition) bool {
	for _, condition := range where {
		match, err := condition.Function.Evaluate(nicRouteSetStrings(row[condition.Column]), nicRouteSetStrings(condition.Value))
		if err != nil || !match {
			return false
		}
	}

	return true
}

func (c *nicRouteCleanupClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	{
		err := ctx.Err()
		if err != nil {
			return nil, err
		}
	}

	c.calls = append(c.calls, slices.Clone(operations))
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return nil, err
	}

	writing := false
	for _, operation := range operations {
		writing = writing || operation.Op == ovsdb.OperationDelete || operation.Op == ovsdb.OperationMutate || operation.Op == ovsdb.OperationUpdate
	}

	if writing && c.beforeWrite != nil {
		hook := c.beforeWrite
		c.beforeWrite = nil
		hook()
	}

	bytes, err := json.Marshal(c.tables)
	if err != nil {
		return nil, err
	}

	var transaction map[string][]ovsdb.Row
	err = json.Unmarshal(bytes, &transaction)
	if err != nil {
		return nil, err
	}

	// RFC7047 also encodes a singleton string set as the scalar string.
	// Normalize only the address-set column, preserving scalar name semantics.
	for _, row := range transaction["Address_Set"] {
		value, ok := row["addresses"].(string)
		if ok {
			row["addresses"] = ovsdb.OvsSet{GoSet: []any{value}}
		}
	}

	results := make([]ovsdb.OperationResult, len(operations))
	for i, operation := range operations {
		rows := transaction[operation.Table]
		selected := []ovsdb.Row{}
		for _, row := range rows {
			if nicRouteRowMatches(row, operation.Where) {
				selection := ovsdb.Row{}
				if operation.Op == ovsdb.OperationSelect && len(operation.Columns) == 0 {
					for column, value := range row {
						selection[column] = value
					}
				}

				for _, column := range operation.Columns {
					selection[column] = row[column]
				}

				selected = append(selected, selection)
			}
		}

		switch operation.Op {
		case ovsdb.OperationSelect:
			results[i].Rows = selected
		case ovsdb.OperationWait:
			expected := operation.Rows
			matches := len(selected) == len(expected)
			if matches {
				for j := range selected {
					for _, column := range operation.Columns {
						matches = matches && reflect.DeepEqual(nicRouteSetStrings(selected[j][column]), nicRouteSetStrings(expected[j][column]))
					}
				}
			}

			if (operation.Until == "==" && !matches) || (operation.Until == "!=" && matches) {
				results[i].Error = "timed out"
				results[i].Details = "inert RFC7047 wait refused"
				return results, nil // The whole transaction is discarded.
			}

		case ovsdb.OperationDelete:
			remaining := []ovsdb.Row{}
			for _, row := range rows {
				if nicRouteRowMatches(row, operation.Where) {
					results[i].Count++
				} else {
					remaining = append(remaining, row)
				}
			}

			transaction[operation.Table] = remaining
		case ovsdb.OperationUpdate:
			for _, row := range rows {
				if !nicRouteRowMatches(row, operation.Where) {
					continue
				}

				for column, value := range operation.Row {
					row[column] = value
				}

				row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				results[i].Count++
			}

		case ovsdb.OperationMutate:
			for _, row := range rows {
				if !nicRouteRowMatches(row, operation.Where) {
					continue
				}

				for _, mutation := range operation.Mutations {
					if (mutation.Column != "static_routes" && mutation.Column != "nat") || mutation.Mutator != ovsdb.MutateOperationDelete {
						return nil, fmt.Errorf("Unsupported inert mutation %v", mutation)
					}

					remove, ok := nicRouteSetStrings(mutation.Value).([]string)
					if !ok {
						return nil, fmt.Errorf("Unsupported inert removal value %T", mutation.Value)
					}

					remaining := []any{}
					set, ok := row[mutation.Column].(ovsdb.OvsSet)
					if !ok {
						return nil, fmt.Errorf("Unsupported inert removal set %T", row[mutation.Column])
					}

					for _, entry := range set.GoSet {
						id, ok := entry.(ovsdb.UUID)
						if !ok {
							return nil, fmt.Errorf("Unsupported inert removal UUID %T", entry)
						}

						if !slices.Contains(remove, id.GoUUID) {
							remaining = append(remaining, entry)
						}
					}

					row[mutation.Column] = ovsdb.OvsSet{GoSet: remaining}
				}

				results[i].Count++
			}

		default:
			return nil, fmt.Errorf("Unsupported inert operation %q", operation.Op)
		}
	}

	if c.truncateNext {
		c.truncateNext = false
		return results[:len(results)-1], nil
	}

	c.tables = transaction
	if writing && c.afterWriteError != nil {
		err := c.afterWriteError
		c.afterWriteError = nil
		return nil, err
	}

	return results, nil
}

func nicRouteCleanupFixture() (*NB, *nicRouteCleanupClient, OVNRouterRoute, string) {
	root := uuid.NewString()
	router := uuid.NewString()
	route := uuid.NewString()
	sibling := uuid.NewString()
	_, prefix, _ := net.ParseCIDR("198.51.100.0/24")
	expected := OVNRouterRoute{Prefix: *prefix, NextHop: net.ParseIP("192.0.2.10"), Port: "incus-net42-lrp-int"}
	makeRow := func(id string, hop string) ovsdb.Row {
		return ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: id}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "ip_prefix": prefix.String(), "nexthop": hop, "output_port": string(expected.Port), "policy": ovsdb.OvsSet{GoSet: []any{}}, "route_table": ""}
	}

	client := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"NB_Global":                   {{"_uuid": ovsdb.UUID{GoUUID: root}}},
		"Logical_Router":              {{"_uuid": ovsdb.UUID{GoUUID: router}, "name": "incus-net42-lr", "static_routes": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: route}, ovsdb.UUID{GoUUID: sibling}}}}},
		"Logical_Router_Static_Route": {makeRow(route, expected.NextHop.String()), makeRow(sibling, "192.0.2.20")},
	}}
	return &NB{client: client, backendID: root}, client, expected, sibling
}

func TestNICRouteCleanupDurableReplayAndSibling(t *testing.T) {
	nb, client, expected, sibling := nicRouteCleanupFixture()
	plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
	require.NoError(t, err)
	require.Len(t, plan.Routes, 1)
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	var reloaded NICRouteCleanup
	require.NoError(t, json.Unmarshal(encoded, &reloaded))
	fresh := &NB{client: client, backendID: nb.backendID}
	require.NoError(t, fresh.ApplyNICRouteCleanup(context.Background(), reloaded))
	require.Len(t, client.tables["Logical_Router_Static_Route"], 1)
	require.Equal(t, sibling, client.tables["Logical_Router_Static_Route"][0]["_uuid"].(ovsdb.UUID).GoUUID)
	ids, err := nicCleanupUUIDSet(client.tables["Logical_Router"][0]["static_routes"])
	require.NoError(t, err)
	require.Equal(t, []string{sibling}, ids)
	// A subsequent same-prefix replacement is retained; replay uses the old UUID, never a new prefix lookup.
	replacement := uuid.NewString()
	newRow := ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: replacement}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "ip_prefix": expected.Prefix.String(), "nexthop": expected.NextHop.String(), "output_port": string(expected.Port), "policy": ovsdb.OvsSet{}, "route_table": ""}
	client.tables["Logical_Router_Static_Route"] = append(client.tables["Logical_Router_Static_Route"], newRow)
	parent := client.tables["Logical_Router"][0]
	parent["static_routes"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: sibling}, ovsdb.UUID{GoUUID: replacement}}}
	require.NoError(t, fresh.ApplyNICRouteCleanup(context.Background(), reloaded))
	require.Len(t, client.tables["Logical_Router_Static_Route"], 2)
	require.Equal(t, replacement, client.tables["Logical_Router_Static_Route"][1]["_uuid"].(ovsdb.UUID).GoUUID)
}

func TestNICRouteCleanupIdentityRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "client-rebound", "router-name-reused", "route-reparented", "foreign-additional-parent"} {
		t.Run(name, func(t *testing.T) {
			nb, client, expected, _ := nicRouteCleanupFixture()
			plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
			require.NoError(t, err)
			switch name {
			case "root-replaced":
				client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "client-rebound":
				nb.backendID = uuid.NewString()
			case "router-name-reused":
				client.tables["Logical_Router"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "route-reparented":
				client.tables["Logical_Router"][0]["static_routes"] = ovsdb.OvsSet{GoSet: []any{client.tables["Logical_Router_Static_Route"][1]["_uuid"]}}
			case "foreign-additional-parent":
				client.tables["Logical_Router"] = append(client.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign", "static_routes": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: plan.Routes[0].UUID}}}})
			}

			before, err := json.Marshal(client.tables)
			require.NoError(t, err)
			require.Error(t, nb.ApplyNICRouteCleanup(context.Background(), plan))
			after, err := json.Marshal(client.tables)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "refused replay cannot delete a sibling/replacement/original row")
		})
	}
}

func TestNICRouteCleanupGuardsConcurrentChange(t *testing.T) {
	for _, name := range []string{"route-relearned", "root-replaced", "foreign-parent-added"} {
		t.Run(name, func(t *testing.T) {
			nb, client, expected, _ := nicRouteCleanupFixture()
			plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
			require.NoError(t, err)
			client.beforeWrite = func() {
				switch name {
				case "route-relearned":
					client.tables["Logical_Router_Static_Route"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
					client.tables["Logical_Router_Static_Route"][0]["nexthop"] = "192.0.2.99"
				case "root-replaced":
					client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				case "foreign-parent-added":
					client.tables["Logical_Router"] = append(client.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign", "static_routes": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: plan.Routes[0].UUID}}}})
				}
			}

			require.Error(t, nb.ApplyNICRouteCleanup(context.Background(), plan))
			require.Len(t, client.tables["Logical_Router_Static_Route"], 2)
		})
	}
}

func TestNICRouteCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"missing-root", "missing-router", "duplicate-matching-route", "shared-original-row", "read-error", "short-response", "missing-next-hop"} {
		t.Run(name, func(t *testing.T) {
			nb, client, expected, _ := nicRouteCleanupFixture()
			switch name {
			case "missing-root":
				nb.backendID = ""
			case "missing-router":
				client.tables["Logical_Router"] = nil
			case "duplicate-matching-route":
				row := ovsdb.Row{}
				for key, value := range client.tables["Logical_Router_Static_Route"][0] {
					row[key] = value
				}

				row["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				client.tables["Logical_Router_Static_Route"] = append(client.tables["Logical_Router_Static_Route"], row)
				parent := client.tables["Logical_Router"][0]
				ids, ok := parent["static_routes"].(ovsdb.OvsSet)
				require.True(t, ok)
				ids.GoSet = append(ids.GoSet, row["_uuid"])
				parent["static_routes"] = ids
			case "shared-original-row":
				client.tables["Logical_Router"] = append(client.tables["Logical_Router"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign", "static_routes": ovsdb.OvsSet{GoSet: []any{client.tables["Logical_Router_Static_Route"][0]["_uuid"]}}})
			case "read-error":
				client.failNext = errors.New("required original route read failed")
			case "short-response":
				client.truncateNext = true
			case "missing-next-hop":
				expected.NextHop = nil
			}

			_, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
			require.Error(t, err)
			for _, call := range client.calls {
				for _, operation := range call {
					require.Contains(t, []string{ovsdb.OperationWait, ovsdb.OperationSelect}, operation.Op, "capture must be read-only")
				}
			}
		})
	}
}

func TestNICRouteCleanupUnacknowledgedWriteReplay(t *testing.T) {
	nb, client, expected, sibling := nicRouteCleanupFixture()
	plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
	require.NoError(t, err)
	client.afterWriteError = errors.New("original route write outcome was not acknowledged")
	require.ErrorContains(t, nb.ApplyNICRouteCleanup(context.Background(), plan), "not acknowledged")
	require.Len(t, client.tables["Logical_Router_Static_Route"], 1)
	require.Equal(t, sibling, client.tables["Logical_Router_Static_Route"][0]["_uuid"].(ovsdb.UUID).GoUUID)
	// Retry the immutable original plan after its write committed but the reply was lost.
	require.NoError(t, nb.ApplyNICRouteCleanup(context.Background(), plan))
	require.Len(t, client.tables["Logical_Router_Static_Route"], 1)
}

func TestNICRouteCleanupPreservesForeignRoutePolicyAndTable(t *testing.T) {
	for _, name := range []string{"source-policy", "foreign-table", "foreign-output-port"} {
		t.Run(name, func(t *testing.T) {
			nb, client, expected, _ := nicRouteCleanupFixture()
			row := client.tables["Logical_Router_Static_Route"][0]
			switch name {
			case "source-policy":
				row["policy"] = "src-ip"
			case "foreign-table":
				row["route_table"] = "foreign"
			case "foreign-output-port":
				row["output_port"] = "foreign-port"
			}

			plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
			require.NoError(t, err)
			require.Empty(t, plan.Routes)
			require.NoError(t, nb.ApplyNICRouteCleanup(context.Background(), plan))
			require.Len(t, client.tables["Logical_Router_Static_Route"], 2)
		})
	}
}

// TestNICRouteCleanupServerRestart covers a stored plan applied after the database server restarted.
func TestNICRouteCleanupServerRestart(t *testing.T) {
	nb, client, expected, _ := nicRouteCleanupFixture()
	plan, err := nb.CaptureNICRouteCleanup(context.Background(), "incus-net42-lr", expected)
	require.NoError(t, err)
	for _, row := range client.tables["Logical_Router_Static_Route"] {
		row["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	}

	require.NoError(t, nb.ApplyNICRouteCleanup(context.Background(), plan))
	require.Len(t, client.tables["Logical_Router_Static_Route"], 1)
}
