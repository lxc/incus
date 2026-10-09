package ovn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICRouteCleanup stores original route identities, not a prefix query to repeat after Stop.
// The caller must capture its actual original router/next-hop/output-port under all relevant reservations.
// This is a leaf of NIC cleanup; it does not acknowledge BGP, MAC bindings, NAT or host hooks.
type NICRouteCleanup struct {
	Version    int
	RootUUID   string
	RouterUUID string
	RouterName OVNRouter
	Routes     []NICRouteCleanupRow
}

// NICRouteCleanupRow binds one original route generation to its expected NIC route inputs.
type NICRouteCleanupRow struct {
	UUID       string
	Version    string
	Prefix     string
	NextHop    string
	OutputPort string
}

func nicCleanupUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func nicCleanupRowUUID(row ovsdb.Row, column string) (string, error) {
	id, ok := row[column].(ovsdb.UUID)
	if !ok || !nicCleanupUUID(id.GoUUID) {
		return "", fmt.Errorf("Invalid NIC cleanup %s", column)
	}

	return id.GoUUID, nil
}

func nicCleanupUUIDSet(value any) ([]string, error) {
	var values []any
	switch value := value.(type) {
	case ovsdb.UUID:
		values = []any{value}
	case ovsdb.OvsSet:
		values = value.GoSet
	default:
		return nil, errors.New("Invalid NIC cleanup UUID set")
	}

	ids := make([]string, 0, len(values))
	for _, value := range values {
		id, ok := value.(ovsdb.UUID)
		if !ok || !nicCleanupUUID(id.GoUUID) || slices.Contains(ids, id.GoUUID) {
			return nil, errors.New("Invalid or duplicate NIC cleanup row UUID")
		}

		ids = append(ids, id.GoUUID)
	}

	return ids, nil
}

func nicCleanupOptionalString(value any) (string, error) {
	{
		text, ok := value.(string)
		if ok {
			return text, nil
		}
	}

	set, ok := value.(ovsdb.OvsSet)
	if !ok || len(set.GoSet) > 1 {
		return "", errors.New("Invalid NIC cleanup optional string")
	}

	if len(set.GoSet) == 0 {
		return "", nil
	}

	text, ok := set.GoSet[0].(string)
	if !ok {
		return "", errors.New("Invalid NIC cleanup optional string value")
	}

	return text, nil
}

func nicCleanupRootWait(root string) ovsdb.Operation {
	zero := 0
	id := ovsdb.UUID{GoUUID: root}
	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "NB_Global", Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}},
	}
}

func nicCleanupRouterWait(routerUUID string, routerName OVNRouter) ovsdb.Operation {
	zero := 0
	id := ovsdb.UUID{GoUUID: routerUUID}
	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "Logical_Router", Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}, {Column: "name", Function: ovsdb.ConditionEqual, Value: string(routerName)}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}},
	}
}

func nicCleanupRouteOwnerWait(routerUUID string, routeUUID string) ovsdb.Operation {
	owner := nicCleanupRouterWait(routerUUID, "")
	owner.Where = []ovsdb.Condition{{Column: "static_routes", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: routeUUID}}}}}
	return owner
}

func (o *NB) nicCleanupTransact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	results, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return nil, err
	}

	_, err = ovsdb.CheckOperationResults(results, operations)
	if err != nil {
		return nil, err
	}

	if len(results) != len(operations) {
		return nil, errors.New("Unexpected NIC cleanup transaction result count")
	}

	return results, nil
}

// CaptureNICRouteCleanup reads an original router and its routes in guarded server transactions.
// A same-prefix sibling with different next-hop/output-port is excluded; duplicate matching owners are refused.
func (o *NB) CaptureNICRouteCleanup(ctx context.Context, routerName OVNRouter, expected ...OVNRouterRoute) (NICRouteCleanup, error) {
	if !nicCleanupUUID(o.backendID) || routerName == "" {
		return NICRouteCleanup{}, errors.New("Original NIC cleanup router/database identity is unavailable")
	}

	for _, route := range expected {
		ones, bits := route.Prefix.Mask.Size()
		if route.NextHop.To16() == nil || route.Port == "" || route.Prefix.IP.To16() == nil || bits == 0 || ones < 0 || route.Discard {
			return NICRouteCleanup{}, errors.New("Original NIC route next-hop/output-port is required")
		}
	}

	root := nicCleanupRootWait(o.backendID)
	results, err := o.nicCleanupTransact(ctx, root, ovsdb.Operation{
		Op: ovsdb.OperationSelect, Table: "Logical_Router",
		Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(routerName)}}, Columns: []string{"_uuid", "name", "static_routes"},
	})
	if err != nil {
		return NICRouteCleanup{}, err
	}

	if len(results[1].Rows) != 1 {
		return NICRouteCleanup{}, errors.New("Original NIC cleanup router is absent or ambiguous")
	}

	router := results[1].Rows[0]
	routerUUID, err := nicCleanupRowUUID(router, "_uuid")
	if err != nil {
		return NICRouteCleanup{}, err
	}

	ids, err := nicCleanupUUIDSet(router["static_routes"])
	if err != nil {
		return NICRouteCleanup{}, err
	}

	// Pin the router's complete membership while its route rows are read. No cached model is an absence proof.
	membership := nicCleanupRouterWait(routerUUID, routerName)
	membership.Columns = []string{"static_routes"}
	membership.Rows = []ovsdb.Row{{"static_routes": router["static_routes"]}}
	operations := []ovsdb.Operation{root, membership}
	for _, id := range ids {
		operations = append(operations, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "Logical_Router_Static_Route",
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}},
			Columns: []string{"_uuid", "_version", "ip_prefix", "nexthop", "output_port", "policy", "route_table"},
		})
	}

	results, err = o.nicCleanupTransact(ctx, operations...)
	if err != nil {
		return NICRouteCleanup{}, err
	}

	plan := NICRouteCleanup{Version: 1, RootUUID: o.backendID, RouterUUID: routerUUID, RouterName: routerName, Routes: []NICRouteCleanupRow{}}
	for _, wanted := range expected {
		var matches []NICRouteCleanupRow
		for i, result := range results[2:] {
			if len(result.Rows) != 1 {
				return NICRouteCleanup{}, errors.New("Original NIC cleanup route changed during capture")
			}

			row := result.Rows[0]
			prefix, prefixOK := row["ip_prefix"].(string)
			nextHop, hopOK := row["nexthop"].(string)
			port, err := nicCleanupOptionalString(row["output_port"])
			if err != nil || !prefixOK || !hopOK {
				return NICRouteCleanup{}, errors.New("Invalid original NIC route inputs")
			}

			policy, err := nicCleanupOptionalString(row["policy"])
			if err != nil {
				return NICRouteCleanup{}, err
			}

			ones, bits := wanted.Prefix.Mask.Size()
			prefixMatches := prefix == wanted.Prefix.String() || (ones == bits && prefix == wanted.Prefix.IP.String())
			table, tableOK := row["route_table"].(string)
			if !tableOK {
				return NICRouteCleanup{}, errors.New("Original NIC route table identity is unavailable")
			}

			if !prefixMatches || nextHop != wanted.NextHop.String() || port != string(wanted.Port) || (policy != "" && policy != "dst-ip") || table != "" {
				continue
			}

			id, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil || id != ids[i] {
				return NICRouteCleanup{}, errors.New("Original NIC route row identity changed")
			}

			version, err := nicCleanupRowUUID(row, "_version")
			if err != nil {
				return NICRouteCleanup{}, err
			}

			matches = append(matches, NICRouteCleanupRow{UUID: id, Version: version, Prefix: prefix, NextHop: nextHop, OutputPort: port})
		}

		if len(matches) > 1 {
			return NICRouteCleanup{}, errors.New("Ambiguous original NIC cleanup route ownership")
		}

		if len(matches) == 1 && !slices.Contains(plan.Routes, matches[0]) {
			plan.Routes = append(plan.Routes, matches[0])
		}
	}

	// Verify the selected versions and their sole original parent in one final read-only transaction.
	proof := []ovsdb.Operation{root, membership}
	for _, route := range plan.Routes {
		id := ovsdb.UUID{GoUUID: route.UUID}
		zero := 0
		proof = append(proof, nicCleanupRouteOwnerWait(plan.RouterUUID, route.UUID))
		proof = append(proof, ovsdb.Operation{
			Op: ovsdb.OperationWait, Table: "Logical_Router_Static_Route", Timeout: &zero,
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}, {Column: "_version", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: route.Version}}},
			Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}},
		})
	}

	proof = append(proof, root)
	_, err = o.nicCleanupTransact(ctx, proof...)
	if err != nil {
		return NICRouteCleanup{}, err
	}

	return plan, nil
}

// ApplyNICRouteCleanup deletes only captured row generations under the same root and original parent.
// Already absent original rows are acknowledged under guarded parent/root identity; replacements are never selected by prefix.
func (o *NB) ApplyNICRouteCleanup(ctx context.Context, plan NICRouteCleanup) error {
	if plan.Version != 1 || !nicCleanupUUID(plan.RootUUID) || plan.RootUUID != o.backendID || !nicCleanupUUID(plan.RouterUUID) || plan.RouterName == "" {
		return errors.New("Original NIC route cleanup database/router identity changed")
	}

	seen := map[string]bool{}
	for _, route := range plan.Routes {
		if !nicCleanupUUID(route.UUID) || !nicCleanupUUID(route.Version) || seen[route.UUID] || route.OutputPort == "" || net.ParseIP(route.NextHop) == nil {
			return errors.New("Invalid original NIC route cleanup row")
		}

		if net.ParseIP(route.Prefix) == nil {
			_, _, err := net.ParseCIDR(route.Prefix)
			if err != nil {
				return errors.New("Invalid original NIC route cleanup prefix")
			}
		}

		seen[route.UUID] = true
	}

	root := nicCleanupRootWait(plan.RootUUID)
	parent := nicCleanupRouterWait(plan.RouterUUID, plan.RouterName)
	reads := []ovsdb.Operation{root, parent}
	for _, route := range plan.Routes {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "Logical_Router_Static_Route",
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: route.UUID}}},
			Columns: []string{"_uuid", "_version", "ip_prefix", "nexthop", "output_port", "route_table"},
		})
	}

	results, err := o.nicCleanupTransact(ctx, reads...)
	if err != nil {
		return err
	}

	operations := []ovsdb.Operation{root, parent}
	zero := 0
	for i, route := range plan.Routes {
		id := ovsdb.UUID{GoUUID: route.UUID}
		where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}}
		row := results[i+2].Rows
		if len(row) == 0 {
			operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Router_Static_Route", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "!=", Rows: []ovsdb.Row{{"_uuid": id}}})
			continue
		}

		if len(row) != 1 {
			return errors.New("Ambiguous original NIC cleanup row")
		}

		// Row versions are server-local and regenerated when a database server restarts, so a stored
		// plan compares the captured content.
		rowID, idErr := nicCleanupRowUUID(row[0], "_uuid")
		port, portErr := nicCleanupOptionalString(row[0]["output_port"])
		if portErr != nil || idErr != nil || rowID != route.UUID || row[0]["route_table"] != "" || row[0]["ip_prefix"] != route.Prefix || row[0]["nexthop"] != route.NextHop || port != route.OutputPort {
			return errors.New("Original NIC cleanup route was modified; retaining cleanup debt")
		}

		// Require exactly one current router owner, the captured parent. A later foreign reference refuses deletion.
		operations = append(operations, nicCleanupRouteOwnerWait(plan.RouterUUID, route.UUID))
		// Row versions are server-local; the leader-executed write guards captured content.
		var outputPort any = ovsdb.OvsSet{GoSet: []any{}}
		if route.OutputPort != "" {
			outputPort = route.OutputPort
		}

		where = append(where,
			ovsdb.Condition{Column: "ip_prefix", Function: ovsdb.ConditionEqual, Value: route.Prefix},
			ovsdb.Condition{Column: "nexthop", Function: ovsdb.ConditionEqual, Value: route.NextHop},
			ovsdb.Condition{Column: "output_port", Function: ovsdb.ConditionEqual, Value: outputPort},
			ovsdb.Condition{Column: "route_table", Function: ovsdb.ConditionEqual, Value: ""})
		operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Router_Static_Route", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}}})
		operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Router_Static_Route", Where: where})
		operations = append(operations, ovsdb.Operation{
			Op: ovsdb.OperationMutate, Table: "Logical_Router", Where: parent.Where,
			Mutations: []ovsdb.Mutation{{Column: "static_routes", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{id}}}},
		})
	}

	operations = append(operations, root)
	results, err = o.nicCleanupTransact(ctx, operations...)
	if err != nil {
		return err
	}

	for i, operation := range operations {
		if (operation.Op == ovsdb.OperationDelete || operation.Op == ovsdb.OperationMutate) && results[i].Count != 1 {
			return errors.New("Original NIC cleanup write was not acknowledged")
		}
	}

	return nil
}
