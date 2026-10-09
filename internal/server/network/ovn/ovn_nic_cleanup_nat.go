package ovn

import (
	"context"
	"errors"
	"net"
	"slices"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICNATCleanup binds a finite set of original NAT row generations and their
// sole router parent. Expected address tuples are supplied by the original NIC
// snapshot; this does not certify ownership of an unattributed legacy row.
type NICNATCleanup struct {
	Version    int
	RootUUID   string
	RouterUUID string
	RouterName OVNRouter
	Rows       []NICNATCleanupRow
}

// NICNATCleanupRow retains the complete translation tuple and original generation.
type NICNATCleanupRow struct {
	UUID       string
	Version    string
	Type       string
	LogicalIP  string
	ExternalIP string
}

// NICNATCleanupTuple is a translation installed for the original NIC address.
// Stop does not select all rows having its external address.
type NICNATCleanupTuple struct {
	Type       string
	LogicalIP  string
	ExternalIP string
}

func nicCleanupNATTupleValid(tuple NICNATCleanupTuple) bool {
	external := net.ParseIP(tuple.ExternalIP)
	if external == nil {
		return false
	}

	var logical net.IP
	switch tuple.Type {
	case "snat":
		ip, prefix, err := net.ParseCIDR(tuple.LogicalIP)
		if err != nil {
			return false
		}

		ones, bits := prefix.Mask.Size()
		if ones != bits || !ip.Equal(prefix.IP) {
			return false
		}

		logical = ip
	case "dnat_and_snat":
		logical = net.ParseIP(tuple.LogicalIP)
	default:
		return false
	}

	return logical != nil && (logical.To4() != nil) == (external.To4() != nil)
}

func nicCleanupNATOwnerWait(routerUUID string, rowUUID string) ovsdb.Operation {
	op := nicCleanupRouterWait(routerUUID, "")
	op.Where = []ovsdb.Condition{{Column: "nat", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: rowUUID}}}}}
	return op
}

// nicCleanupNATWhere guards the original row. Row versions are server-local, so
// they are only usable in read-only proofs; writes run on the cluster leader.
func nicCleanupNATWhere(row NICNATCleanupRow, version bool) []ovsdb.Condition {
	where := []ovsdb.Condition{
		{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.UUID}},
		{Column: "type", Function: ovsdb.ConditionEqual, Value: row.Type},
		{Column: "logical_ip", Function: ovsdb.ConditionEqual, Value: row.LogicalIP},
		{Column: "external_ip", Function: ovsdb.ConditionEqual, Value: row.ExternalIP},
	}

	if version {
		where = append(where, ovsdb.Condition{Column: "_version", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.Version}})
	}

	return where
}

// CaptureNICNATCleanup uses server reads and final guards to retain original
// matching NAT rows. Same-external-address foreign translations are excluded;
// ambiguous matches or shared references refuse capture.
func (o *NB) CaptureNICNATCleanup(ctx context.Context, routerName OVNRouter, expected ...NICNATCleanupTuple) (NICNATCleanup, error) {
	if !nicCleanupUUID(o.backendID) || routerName == "" {
		return NICNATCleanup{}, errors.New("Original NIC NAT database/router identity is unavailable")
	}

	for _, tuple := range expected {
		if !nicCleanupNATTupleValid(tuple) {
			return NICNATCleanup{}, errors.New("Original NIC NAT address tuple is invalid")
		}
	}

	root := nicCleanupRootWait(o.backendID)
	results, err := o.nicCleanupTransact(ctx, root, ovsdb.Operation{
		Op: ovsdb.OperationSelect, Table: "Logical_Router",
		Where:   []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(routerName)}},
		Columns: []string{"_uuid", "name", "nat"},
	})
	if err != nil {
		return NICNATCleanup{}, err
	}

	if len(results[1].Rows) != 1 {
		return NICNATCleanup{}, errors.New("Original NIC NAT router is absent or ambiguous")
	}

	router := results[1].Rows[0]
	routerUUID, err := nicCleanupRowUUID(router, "_uuid")
	if err != nil {
		return NICNATCleanup{}, err
	}

	ids, err := nicCleanupUUIDSet(router["nat"])
	if err != nil {
		return NICNATCleanup{}, err
	}

	membership := nicCleanupRouterWait(routerUUID, routerName)
	membership.Columns = []string{"nat"}
	membership.Rows = []ovsdb.Row{{"nat": router["nat"]}}
	reads := []ovsdb.Operation{root, membership}
	for _, id := range ids {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "NAT",
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}},
			Columns: []string{"_uuid", "_version", "type", "logical_ip", "external_ip"},
		})
	}

	results, err = o.nicCleanupTransact(ctx, reads...)
	if err != nil {
		return NICNATCleanup{}, err
	}

	plan := NICNATCleanup{Version: 1, RootUUID: o.backendID, RouterUUID: routerUUID, RouterName: routerName, Rows: []NICNATCleanupRow{}}
	for _, tuple := range expected {
		var matches []NICNATCleanupRow
		for i, result := range results[2:] {
			if len(result.Rows) != 1 {
				return NICNATCleanup{}, errors.New("Original NIC NAT membership changed during capture")
			}

			row := result.Rows[0]
			kind, kindOK := row["type"].(string)
			logical, logicalOK := row["logical_ip"].(string)
			external, externalOK := row["external_ip"].(string)
			if !kindOK || !logicalOK || !externalOK {
				return NICNATCleanup{}, errors.New("Invalid original NIC NAT translation")
			}

			if kind != tuple.Type || logical != tuple.LogicalIP || external != tuple.ExternalIP {
				continue
			}

			id, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil || id != ids[i] {
				return NICNATCleanup{}, errors.New("Original NIC NAT row identity changed")
			}

			version, err := nicCleanupRowUUID(row, "_version")
			if err != nil {
				return NICNATCleanup{}, err
			}

			matches = append(matches, NICNATCleanupRow{UUID: id, Version: version, Type: kind, LogicalIP: logical, ExternalIP: external})
		}

		if len(matches) > 1 {
			return NICNATCleanup{}, errors.New("Ambiguous original NIC NAT translation")
		}

		if len(matches) == 1 && !slices.Contains(plan.Rows, matches[0]) {
			plan.Rows = append(plan.Rows, matches[0])
		}
	}

	proof := []ovsdb.Operation{root, membership}
	zero := 0
	for _, row := range plan.Rows {
		proof = append(proof, nicCleanupNATOwnerWait(routerUUID, row.UUID),
			ovsdb.Operation{
				Op: ovsdb.OperationWait, Table: "NAT", Timeout: &zero, Where: nicCleanupNATWhere(row, true),
				Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: row.UUID}}},
			})
	}

	proof = append(proof, root)
	_, err = o.nicCleanupTransact(ctx, proof...)
	if err != nil {
		return NICNATCleanup{}, err
	}

	return plan, nil
}

// ApplyNICNATCleanup deletes only immutable captured rows, never a current
// external-IP search. Changed generations and later foreign references refuse
// deletion; guarded old-row absence leaves replacements untouched.
func (o *NB) ApplyNICNATCleanup(ctx context.Context, plan NICNATCleanup) error {
	if plan.Version != 1 || plan.RootUUID != o.backendID || !nicCleanupUUID(plan.RootUUID) || !nicCleanupUUID(plan.RouterUUID) || plan.RouterName == "" {
		return errors.New("Original NIC NAT cleanup database/router identity changed")
	}

	seen := map[string]bool{}
	for _, row := range plan.Rows {
		if !nicCleanupUUID(row.UUID) || !nicCleanupUUID(row.Version) || seen[row.UUID] || !nicCleanupNATTupleValid(NICNATCleanupTuple{Type: row.Type, LogicalIP: row.LogicalIP, ExternalIP: row.ExternalIP}) {
			return errors.New("Invalid original NIC NAT cleanup row")
		}

		seen[row.UUID] = true
	}

	root := nicCleanupRootWait(plan.RootUUID)
	parent := nicCleanupRouterWait(plan.RouterUUID, plan.RouterName)
	reads := []ovsdb.Operation{root, parent}
	for _, row := range plan.Rows {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "NAT",
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.UUID}}},
			Columns: []string{"_uuid", "_version", "type", "logical_ip", "external_ip"},
		})
	}

	results, err := o.nicCleanupTransact(ctx, reads...)
	if err != nil {
		return err
	}

	operations := []ovsdb.Operation{root, parent}
	zero := 0
	for i, row := range plan.Rows {
		id := ovsdb.UUID{GoUUID: row.UUID}
		selected := results[i+2].Rows
		if len(selected) == 0 {
			operations = append(operations, ovsdb.Operation{
				Op: ovsdb.OperationWait, Table: "NAT", Timeout: &zero,
				Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}},
				Columns: []string{"_uuid"}, Until: "!=", Rows: []ovsdb.Row{{"_uuid": id}},
			})
			continue
		}

		if len(selected) != 1 || !nicCleanupNATRowMatches(selected[0], row) {
			return errors.New("Original NIC NAT row was modified; retaining cleanup debt")
		}

		where := nicCleanupNATWhere(row, false)
		operations = append(operations, nicCleanupNATOwnerWait(plan.RouterUUID, row.UUID),
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "NAT", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}}},
			ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NAT", Where: where},
			ovsdb.Operation{
				Op: ovsdb.OperationMutate, Table: "Logical_Router", Where: parent.Where,
				Mutations: []ovsdb.Mutation{{Column: "nat", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{id}}}},
			})
	}

	operations = append(operations, root)
	results, err = o.nicCleanupTransact(ctx, operations...)
	if err != nil {
		return err
	}

	for i, op := range operations {
		if (op.Op == ovsdb.OperationDelete || op.Op == ovsdb.OperationMutate) && results[i].Count != 1 {
			return errors.New("Original NIC NAT cleanup write was not acknowledged")
		}
	}

	return nil
}

// nicCleanupNATRowMatches compares a stored plan with captured content; row versions are
// server-local and regenerated when a database server restarts.
func nicCleanupNATRowMatches(selected ovsdb.Row, row NICNATCleanupRow) bool {
	id, idErr := nicCleanupRowUUID(selected, "_uuid")
	return idErr == nil && id == row.UUID && selected["type"] == row.Type && selected["logical_ip"] == row.LogicalIP && selected["external_ip"] == row.ExternalIP
}
