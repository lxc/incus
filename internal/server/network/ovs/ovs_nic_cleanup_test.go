package ovs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type nicOVSCleanupClient struct {
	ovsdbClient.Client
	tables          map[string][]ovsdb.Row
	calls           [][]ovsdb.Operation
	beforeWrite     func()
	failNext        error
	truncateNext    bool
	afterWriteError error
}

func nicOVSSetStrings(value any) any {
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

func nicOVSRowMatches(row ovsdb.Row, where []ovsdb.Condition) bool {
	for _, condition := range where {
		match, err := condition.Function.Evaluate(nicOVSSetStrings(row[condition.Column]), nicOVSSetStrings(condition.Value))
		if err != nil || !match {
			return false
		}
	}

	return true
}

func (c *nicOVSCleanupClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
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

	results := make([]ovsdb.OperationResult, len(operations))
	for i, operation := range operations {
		rows := transaction[operation.Table]
		selected := []ovsdb.Row{}
		for _, row := range rows {
			if nicOVSRowMatches(row, operation.Where) {
				selection := ovsdb.Row{}
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
						matches = matches && reflect.DeepEqual(nicOVSSetStrings(selected[j][column]), nicOVSSetStrings(expected[j][column]))
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
				if nicOVSRowMatches(row, operation.Where) {
					results[i].Count++
				} else {
					remaining = append(remaining, row)
				}
			}

			transaction[operation.Table] = remaining
		case ovsdb.OperationUpdate:
			for _, row := range rows {
				if !nicOVSRowMatches(row, operation.Where) {
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
				if !nicOVSRowMatches(row, operation.Where) {
					continue
				}

				for _, mutation := range operation.Mutations {
					if mutation.Column != "ports" || mutation.Mutator != ovsdb.MutateOperationDelete {
						return nil, fmt.Errorf("Unsupported inert mutation %v", mutation)
					}

					remove, ok := nicOVSSetStrings(mutation.Value).([]string)
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

	if writing {
		c.tables = transaction
	}

	if writing && c.afterWriteError != nil {
		err := c.afterWriteError
		c.afterWriteError = nil
		return nil, err
	}

	return results, nil
}

func nicOVSPortFixture() (*VSwitch, *nicOVSCleanupClient, string) {
	root, bridge, port, iface := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	siblingPort, siblingIface := uuid.NewString(), uuid.NewString()
	client := &nicOVSCleanupClient{tables: map[string][]ovsdb.Row{
		"Open_vSwitch": {{"_uuid": ovsdb.UUID{GoUUID: root}, "bridges": nicPortSet(bridge), "external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}}},
		"Bridge":       {{"_uuid": ovsdb.UUID{GoUUID: bridge}, "name": "br-original", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}, ovsdb.UUID{GoUUID: siblingPort}}}}},
		"Port": {
			{"_uuid": ovsdb.UUID{GoUUID: port}, "name": "original-host", "interfaces": nicPortSet(iface)},
			{"_uuid": ovsdb.UUID{GoUUID: siblingPort}, "name": "sibling-host", "interfaces": nicPortSet(siblingIface)},
		},
		"Interface": {
			{"_uuid": ovsdb.UUID{GoUUID: iface}, "name": "original-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "original-ovn-port", "foreign": "kept"}}},
			{"_uuid": ovsdb.UUID{GoUUID: siblingIface}, "name": "sibling-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "sibling-ovn-port"}}},
		},
	}}
	return &VSwitch{client: client, backendID: root, rootUUID: root}, client, siblingPort
}

func captureNICOVSPort(t *testing.T, sw *VSwitch) NICPortCleanup {
	t.Helper()
	plan, err := sw.CaptureNICPortCleanup(context.Background(), "br-original", "original-host", "original-ovn-port")
	require.NoError(t, err)
	return plan
}

func TestNICOVSPortCleanupStoredReplayAndSibling(t *testing.T) {
	sw, client, sibling := nicOVSPortFixture()
	plan := captureNICOVSPort(t, sw)
	for _, call := range client.calls {
		for _, op := range call {
			require.Contains(t, []string{ovsdb.OperationSelect, ovsdb.OperationWait}, op.Op)
		}
	}

	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICPortCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &VSwitch{client: client, backendID: sw.backendID}
	// Statistics/version updates do not change the original ownership fields.
	client.tables["Interface"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	require.NoError(t, fresh.ApplyNICPortCleanup(context.Background(), stored))
	require.Len(t, client.tables["Port"], 1)
	require.Len(t, client.tables["Interface"], 1)
	require.Equal(t, sibling, client.tables["Port"][0]["_uuid"].(ovsdb.UUID).GoUUID)
	require.Equal(t, "sibling-host", client.tables["Interface"][0]["name"])
	require.NoError(t, fresh.ApplyNICPortCleanup(context.Background(), stored))
	// Reuse of the original name creates new row identities; replay preserves it.
	replacementPort, replacementIface := uuid.NewString(), uuid.NewString()
	client.tables["Port"] = append(client.tables["Port"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: replacementPort}, "name": "original-host", "interfaces": nicPortSet(replacementIface)})
	client.tables["Interface"] = append(client.tables["Interface"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: replacementIface}, "name": "original-host", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "foreign-ovn-port"}}})
	client.tables["Bridge"][0]["ports"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: sibling}, ovsdb.UUID{GoUUID: replacementPort}}}
	require.NoError(t, fresh.ApplyNICPortCleanup(context.Background(), stored))
	require.Len(t, client.tables["Port"], 2)
	require.Len(t, client.tables["Interface"], 2)
	require.Equal(t, replacementPort, client.tables["Port"][1]["_uuid"].(ovsdb.UUID).GoUUID)
	require.Equal(t, replacementIface, client.tables["Interface"][1]["_uuid"].(ovsdb.UUID).GoUUID)
}

func TestNICOVSPortCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"foreign-iface-id", "bridge-not-rooted", "foreign-bridge", "foreign-port", "bonded-port", "missing-bridge", "missing-port", "missing-interface", "ambiguous-port", "unfenced-client", "root-replaced", "extra-root", "read-error", "short-response", "nil-context"} {
		t.Run(name, func(t *testing.T) {
			sw, client, _ := nicOVSPortFixture()
			portID := client.tables["Port"][0]["_uuid"].(ovsdb.UUID).GoUUID
			ifaceID := client.tables["Interface"][0]["_uuid"].(ovsdb.UUID).GoUUID
			ctx := context.Background()
			switch name {
			case "foreign-iface-id":
				client.tables["Interface"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "foreign-ovn-port"}}
			case "bridge-not-rooted":
				client.tables["Open_vSwitch"][0]["bridges"] = ovsdb.OvsSet{}
			case "foreign-bridge":
				client.tables["Bridge"] = append(client.tables["Bridge"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-bridge", "ports": nicPortSet(portID)})
			case "foreign-port":
				client.tables["Port"] = append(client.tables["Port"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-port", "interfaces": nicPortSet(ifaceID)})
			case "bonded-port":
				client.tables["Port"][0]["interfaces"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: ifaceID}, ovsdb.UUID{GoUUID: uuid.NewString()}}}
			case "missing-bridge":
				client.tables["Bridge"] = nil
			case "missing-port":
				client.tables["Port"] = client.tables["Port"][1:]
			case "missing-interface":
				client.tables["Interface"] = client.tables["Interface"][1:]
			case "ambiguous-port":
				client.tables["Port"] = append(client.tables["Port"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "original-host"})
			case "unfenced-client":
				sw.backendID = ""
			case "root-replaced":
				client.tables["Open_vSwitch"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "extra-root":
				client.tables["Open_vSwitch"] = append(client.tables["Open_vSwitch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}})
			case "read-error":
				client.failNext = errors.New("inert OVS read failed")
			case "short-response":
				client.truncateNext = true
			case "nil-context":
				ctx = nil
			}

			_, err := sw.CaptureNICPortCleanup(ctx, "br-original", "original-host", "original-ovn-port")
			require.Error(t, err)
			for _, call := range client.calls {
				for _, op := range call {
					require.Contains(t, []string{ovsdb.OperationWait, ovsdb.OperationSelect}, op.Op)
				}
			}
		})
	}
}

func TestNICOVSPortCleanupReplayRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "client-rebound", "bridge-name-reused", "port-renamed", "interface-renamed", "iface-id-changed", "foreign-bridge", "foreign-port", "interface-replaced", "missing-interface"} {
		t.Run(name, func(t *testing.T) {
			sw, client, _ := nicOVSPortFixture()
			plan := captureNICOVSPort(t, sw)
			switch name {
			case "root-replaced":
				client.tables["Open_vSwitch"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "client-rebound":
				sw.backendID = uuid.NewString()
			case "bridge-name-reused":
				client.tables["Bridge"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "port-renamed":
				client.tables["Port"][0]["name"] = "foreign-name"
			case "interface-renamed":
				client.tables["Interface"][0]["name"] = "foreign-name"
			case "iface-id-changed":
				client.tables["Interface"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "target-port"}}
			case "foreign-bridge":
				client.tables["Bridge"] = append(client.tables["Bridge"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-bridge", "ports": nicPortSet(plan.PortUUID)})
			case "foreign-port":
				client.tables["Port"] = append(client.tables["Port"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-port", "interfaces": nicPortSet(plan.InterfaceUUID)})
			case "interface-replaced":
				id := uuid.NewString()
				client.tables["Interface"][0]["_uuid"] = ovsdb.UUID{GoUUID: id}
				client.tables["Port"][0]["interfaces"] = nicPortSet(id)
			case "missing-interface":
				client.tables["Interface"] = client.tables["Interface"][1:]
			}

			before := nicOVSNormalizedTables(client.tables)
			require.Error(t, sw.ApplyNICPortCleanup(context.Background(), plan))
			require.Equal(t, before, nicOVSNormalizedTables(client.tables))
		})
	}
}

func TestNICOVSPortCleanupGuardAtDispatch(t *testing.T) {
	sw, client, _ := nicOVSPortFixture()
	plan := captureNICOVSPort(t, sw)
	client.beforeWrite = func() {
		client.tables["Interface"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"iface-id": "target-port"}}
	}

	require.Error(t, sw.ApplyNICPortCleanup(context.Background(), plan))
	require.Len(t, client.tables["Port"], 2)
	require.Len(t, client.tables["Interface"], 2)
	ids, ok := nicOVSSetStrings(client.tables["Interface"][0]["external_ids"]).(map[string]string)
	require.True(t, ok)
	require.Equal(t, "target-port", ids["iface-id"])
}

func TestNICOVSPortCleanupLostReply(t *testing.T) {
	sw, client, _ := nicOVSPortFixture()
	plan := captureNICOVSPort(t, sw)
	client.afterWriteError = errors.New("inert lost OVS reply")
	require.ErrorContains(t, sw.ApplyNICPortCleanup(context.Background(), plan), "lost OVS reply")
	require.Len(t, client.tables["Port"], 1)
	require.Len(t, client.tables["Interface"], 1)
	require.NoError(t, (&VSwitch{client: client, backendID: sw.backendID}).ApplyNICPortCleanup(context.Background(), plan))
}

// Normalize RFC7047 sets/maps, whose wire ordering is not ownership or state.
func nicOVSNormalizedTables(tables map[string][]ovsdb.Row) map[string][]map[string]any {
	result := make(map[string][]map[string]any, len(tables))
	for table, rows := range tables {
		for _, row := range rows {
			values := make(map[string]any, len(row))
			for column, value := range row {
				values[column] = nicOVSSetStrings(value)
			}

			result[table] = append(result[table], values)
		}
	}

	return result
}
