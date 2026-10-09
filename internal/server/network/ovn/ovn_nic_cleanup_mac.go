package ovn

import (
	"context"
	"errors"
	"net"
	"slices"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICMACBindingCleanup retains original dynamic cache row generations and the
// SB datapath associated with the captured NB router. It is only cache
// invalidation evidence, never workload fencing or a complete NIC acknowledgment.
type NICMACBindingCleanup struct {
	Version         int
	RootUUID        string
	NBRootUUID      string
	NBRouterUUID    string
	DatapathUUID    string
	DatapathVersion string
	Port            OVNRouterPort
	Rows            []NICMACBindingCleanupRow
}

// NICMACBindingCleanupRow identifies one original dynamic MAC cache generation.
type NICMACBindingCleanupRow struct {
	UUID    string
	Version string
	IP      string

	// MAC is the learned address at capture; plans stored before it existed omit it.
	MAC string `json:",omitempty"`
}

// Validate checks representation only; it grants no backend or source ownership.
func (p NICMACBindingCleanup) Validate() error {
	if p.Version != 1 || p.Port == "" {
		return errors.New("Original NIC MAC binding plan is unsupported or incomplete")
	}

	for _, id := range []string{p.RootUUID, p.NBRootUUID, p.NBRouterUUID, p.DatapathUUID, p.DatapathVersion} {
		if !nicCleanupUUID(id) {
			return errors.New("Invalid original NIC MAC binding database/datapath identity")
		}
	}

	seen := map[string]bool{}
	for _, row := range p.Rows {
		if !nicCleanupUUID(row.UUID) || !nicCleanupUUID(row.Version) || net.ParseIP(row.IP) == nil || seen[row.UUID] {
			return errors.New("Invalid original NIC MAC binding row identity")
		}

		seen[row.UUID] = true
	}

	return nil
}

func nicCleanupSBRootWait(root string) ovsdb.Operation {
	op := nicCleanupRootWait(root)
	op.Table = "SB_Global"
	return op
}

// nicCleanupMACDatapathWait guards the original router datapath. Row versions are
// server-local, so only read-only proofs on the capturing server may include them.
func nicCleanupMACDatapathWait(plan NICMACBindingCleanup, version bool) ovsdb.Operation {
	zero := 0
	id := ovsdb.UUID{GoUUID: plan.DatapathUUID}
	where := []ovsdb.Condition{
		{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id},
		{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{"logical-router": plan.NBRouterUUID}}},
	}

	if version {
		where = append(where, ovsdb.Condition{Column: "_version", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: plan.DatapathVersion}})
	}

	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "Datapath_Binding", Timeout: &zero,
		Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}},
	}
}

// nicCleanupMACContentWhere guards a cache row by identity and observed MAC for leader-executed writes.
func nicCleanupMACContentWhere(plan NICMACBindingCleanup, row NICMACBindingCleanupRow, mac any) []ovsdb.Condition {
	return []ovsdb.Condition{
		{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.UUID}},
		{Column: "datapath", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: plan.DatapathUUID}},
		{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: string(plan.Port)},
		{Column: "ip", Function: ovsdb.ConditionEqual, Value: row.IP},
		{Column: "mac", Function: ovsdb.ConditionEqual, Value: mac},
	}
}

func nicCleanupMACWhere(plan NICMACBindingCleanup, row NICMACBindingCleanupRow) []ovsdb.Condition {
	return []ovsdb.Condition{
		{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.UUID}},
		{Column: "_version", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.Version}},
		{Column: "datapath", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: plan.DatapathUUID}},
		{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: string(plan.Port)},
		{Column: "ip", Function: ovsdb.ConditionEqual, Value: row.IP},
	}
}

func (o *SB) nicCleanupTransact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	results, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return nil, err
	}

	_, err = ovsdb.CheckOperationResults(results, operations)
	if err != nil {
		return nil, err
	}

	if len(results) != len(operations) {
		return nil, errors.New("Unexpected NIC MAC binding transaction result count")
	}

	return results, nil
}

// CaptureNICMACBindingCleanup reads only the original router datapath and
// port/IP cache generations. Its NB relationship uses northd's logical-router
// external ID, not a reused datapath or router-port name alone.
func (o *SB) CaptureNICMACBindingCleanup(ctx context.Context, nbRoot string, routerUUID string, port OVNRouterPort, ips ...net.IP) (NICMACBindingCleanup, error) {
	if !nicCleanupUUID(o.backendID) || !nicCleanupUUID(nbRoot) || !nicCleanupUUID(routerUUID) || port == "" {
		return NICMACBindingCleanup{}, errors.New("Original NIC MAC binding source identity is missing")
	}

	var addresses []string
	for _, ip := range ips {
		if ip.To16() == nil {
			return NICMACBindingCleanup{}, errors.New("Invalid original NIC MAC binding IP")
		}

		if !slices.Contains(addresses, ip.String()) {
			addresses = append(addresses, ip.String())
		}
	}

	root := nicCleanupSBRootWait(o.backendID)
	results, err := o.nicCleanupTransact(ctx, root, ovsdb.Operation{
		Op: ovsdb.OperationSelect, Table: "Datapath_Binding",
		Where:   []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{"logical-router": routerUUID}}}},
		Columns: []string{"_uuid", "_version", "external_ids"},
	})
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	if len(results[1].Rows) != 1 {
		return NICMACBindingCleanup{}, errors.New("Original NIC MAC router datapath is absent or ambiguous")
	}

	datapath := results[1].Rows[0]
	id, err := nicCleanupRowUUID(datapath, "_uuid")
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	version, err := nicCleanupRowUUID(datapath, "_version")
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	plan := NICMACBindingCleanup{Version: 1, RootUUID: o.backendID, NBRootUUID: nbRoot, NBRouterUUID: routerUUID, DatapathUUID: id, DatapathVersion: version, Port: port, Rows: []NICMACBindingCleanupRow{}}
	parent := nicCleanupMACDatapathWait(plan, true)
	reads := []ovsdb.Operation{root, parent}
	for _, ip := range addresses {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "MAC_Binding",
			Where: []ovsdb.Condition{
				{Column: "datapath", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}},
				{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: string(port)},
				{Column: "ip", Function: ovsdb.ConditionEqual, Value: ip},
			}, Columns: []string{"_uuid", "_version", "datapath", "logical_port", "ip", "mac"},
		})
	}

	results, err = o.nicCleanupTransact(ctx, reads...)
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	for i, result := range results[2:] {
		if len(result.Rows) > 1 {
			return NICMACBindingCleanup{}, errors.New("Ambiguous original NIC MAC cache generation")
		}

		if len(result.Rows) == 0 {
			continue
		}

		row := result.Rows[0]
		rowID, err := nicCleanupRowUUID(row, "_uuid")
		if err != nil {
			return NICMACBindingCleanup{}, err
		}

		rowVersion, err := nicCleanupRowUUID(row, "_version")
		if err != nil {
			return NICMACBindingCleanup{}, err
		}

		mac, ok := row["mac"].(string)
		if !ok {
			return NICMACBindingCleanup{}, errors.New("Invalid original NIC MAC cache address")
		}

		plan.Rows = append(plan.Rows, NICMACBindingCleanupRow{UUID: rowID, Version: rowVersion, IP: addresses[i], MAC: mac})
	}

	err = plan.Validate()
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	zero := 0
	proof := []ovsdb.Operation{root, parent}
	for _, row := range plan.Rows {
		proof = append(proof, ovsdb.Operation{
			Op: ovsdb.OperationWait, Table: "MAC_Binding", Timeout: &zero, Where: nicCleanupMACWhere(plan, row),
			Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: row.UUID}}},
		})
	}

	proof = append(proof, root)
	_, err = o.nicCleanupTransact(ctx, proof...)
	if err != nil {
		return NICMACBindingCleanup{}, err
	}

	return plan, nil
}

// ApplyNICMACBindingCleanup invalidates the stored original cache rows. Rows are matched by
// identity and learned MAC, not by their server-local _version, which changes on a database
// restart. A replacement or relearned row is retained.
func (o *SB) ApplyNICMACBindingCleanup(ctx context.Context, plan NICMACBindingCleanup) error {
	err := plan.Validate()
	if err != nil {
		return err
	}

	if plan.RootUUID != o.backendID {
		return errors.New("Original NIC MAC binding database identity changed")
	}

	root := nicCleanupSBRootWait(plan.RootUUID)
	parent := nicCleanupMACDatapathWait(plan, false)
	reads := []ovsdb.Operation{root, parent}
	for _, row := range plan.Rows {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "MAC_Binding",
			Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: row.UUID}}},
			Columns: []string{"_uuid", "_version", "datapath", "logical_port", "ip", "mac"},
		})
	}

	results, err := o.nicCleanupTransact(ctx, reads...)
	if err != nil {
		return err
	}

	zero := 0
	operations := []ovsdb.Operation{root, parent}
	for i, row := range plan.Rows {
		selected := results[i+2].Rows
		if len(selected) > 1 {
			return errors.New("Ambiguous original NIC MAC binding row")
		}

		if len(selected) == 0 {
			continue
		}

		// A row reused for another tuple, or relearned with another MAC, is retained.
		if selected[0]["_uuid"] != (ovsdb.UUID{GoUUID: row.UUID}) || selected[0]["datapath"] != (ovsdb.UUID{GoUUID: plan.DatapathUUID}) || selected[0]["logical_port"] != string(plan.Port) || selected[0]["ip"] != row.IP {
			continue
		}

		if row.MAC != "" && selected[0]["mac"] != row.MAC {
			continue
		}

		// The leader executes this write; guard the locally verified content, not its server-local version.
		where := nicCleanupMACContentWhere(plan, row, selected[0]["mac"])
		operations = append(operations, ovsdb.Operation{
			Op: ovsdb.OperationWait, Table: "MAC_Binding", Timeout: &zero, Where: where,
			Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: row.UUID}}},
		})
		operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "MAC_Binding", Where: where})
	}

	operations = append(operations, root)
	results, err = o.nicCleanupTransact(ctx, operations...)
	if err != nil {
		return err
	}

	for i, op := range operations {
		if op.Op == ovsdb.OperationDelete && results[i].Count != 1 {
			return errors.New("Original NIC MAC cache deletion was not acknowledged")
		}
	}

	return nil
}

// VerifyNICMigrationMACBindings guards the original SB root and cache generations.
// Migration preserves these rows; a cache refresh is retained and reported as changed.
func (o *SB) VerifyNICMigrationMACBindings(ctx context.Context, plan NICMACBindingCleanup) error {
	err := plan.Validate()
	if err != nil {
		return err
	}

	if plan.RootUUID != o.backendID {
		return errors.New("Migration SB root changed")
	}

	root, parent := nicCleanupSBRootWait(plan.RootUUID), nicCleanupMACDatapathWait(plan, false)
	zero := 0
	operations := []ovsdb.Operation{root, parent}
	for _, original := range plan.Rows {
		where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: original.UUID}}}
		results, err := o.nicCleanupTransact(ctx, root, parent, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "MAC_Binding", Where: where, Columns: []string{"_uuid", "_version", "datapath", "logical_port", "ip"}})
		if err != nil {
			return err
		}

		rows := results[2].Rows
		if len(rows) > 1 {
			return errors.New("Ambiguous migration cache row")
		}

		if len(rows) == 0 {
			operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationWait, Table: "MAC_Binding", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{}})
			continue
		}

		row := rows[0]
		if row["datapath"] != (ovsdb.UUID{GoUUID: plan.DatapathUUID}) || row["logical_port"] != string(plan.Port) || row["ip"] != original.IP {
			return errors.New("Migration cache parent/tuple changed")
		}
		// A controller refresh remains untouched and is guarded by its observed content.
		operations = append(operations, nicMigrationRowWait("MAC_Binding", row, false))
	}

	_, err = o.nicCleanupTransact(ctx, operations...)
	return err
}
