package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICPortCleanup binds administrative disable to one original switch/port row
// and source location. It is not a whole NIC cleanup acknowledgment.
type NICPortCleanup struct {
	Version     int
	RootUUID    string
	SwitchUUID  string
	SwitchName  OVNSwitch
	PortUUID    string
	PortVersion string
	PortName    OVNSwitchPort
	Source      string
}

func nicCleanupPortParentWait(plan NICPortCleanup) ovsdb.Operation {
	zero := 0
	id := ovsdb.UUID{GoUUID: plan.SwitchUUID}
	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "Logical_Switch", Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}, {Column: "name", Function: ovsdb.ConditionEqual, Value: string(plan.SwitchName)}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}},
	}
}

func nicCleanupPortOwnerWait(plan NICPortCleanup) ovsdb.Operation {
	op := nicCleanupPortParentWait(plan)
	op.Where = []ovsdb.Condition{{Column: "ports", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: plan.PortUUID}}}}}
	return op
}

func nicCleanupStringMap(value any) (map[string]string, error) {
	wire, ok := value.(ovsdb.OvsMap)
	if !ok {
		return nil, errors.New("Invalid NIC cleanup string map")
	}

	result := make(map[string]string, len(wire.GoMap))
	for key, value := range wire.GoMap {
		k, okKey := key.(string)
		v, okValue := value.(string)
		if !okKey || !okValue {
			return nil, errors.New("Invalid NIC cleanup string map entry")
		}

		result[k] = v
	}

	return result, nil
}

func nicCleanupPortEnabled(value any) (bool, error) {
	{
		b, ok := value.(bool)
		if ok {
			return b, nil
		}
	}

	set, ok := value.(ovsdb.OvsSet)
	if !ok || len(set.GoSet) > 1 {
		return false, errors.New("Invalid NIC cleanup enabled state")
	}

	if len(set.GoSet) == 0 {
		return true, nil
	}

	b, ok := set.GoSet[0].(bool)
	if !ok {
		return false, errors.New("Invalid NIC cleanup enabled value")
	}

	return b, nil
}

// CaptureNICPortCleanup reads server rows and proves their original parent,
// source and version before any mutation. Cached names are not an identity proof.
func (o *NB) CaptureNICPortCleanup(ctx context.Context, switchName OVNSwitch, portName OVNSwitchPort, source string) (NICPortCleanup, error) {
	if !nicCleanupUUID(o.backendID) || switchName == "" || portName == "" || source == "" {
		return NICPortCleanup{}, errors.New("Original NIC port source/database identity is missing")
	}

	root := nicCleanupRootWait(o.backendID)
	results, err := o.nicCleanupTransact(ctx, root,
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(switchName)}}, Columns: []string{"_uuid", "name", "ports"}},
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(portName)}}, Columns: []string{"_uuid", "_version", "name", "external_ids", "enabled"}})
	if err != nil {
		return NICPortCleanup{}, err
	}

	if len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		return NICPortCleanup{}, errors.New("Original NIC switch/port is absent or ambiguous")
	}

	switchID, err := nicCleanupRowUUID(results[1].Rows[0], "_uuid")
	if err != nil {
		return NICPortCleanup{}, err
	}

	portID, err := nicCleanupRowUUID(results[2].Rows[0], "_uuid")
	if err != nil {
		return NICPortCleanup{}, err
	}

	version, err := nicCleanupRowUUID(results[2].Rows[0], "_version")
	if err != nil {
		return NICPortCleanup{}, err
	}

	external, err := nicCleanupStringMap(results[2].Rows[0]["external_ids"])
	if err != nil || external[ovnExtIDIncusLocation] != source {
		return NICPortCleanup{}, errors.New("Original NIC port source location changed")
	}

	plan := NICPortCleanup{Version: 1, RootUUID: o.backendID, SwitchUUID: switchID, SwitchName: switchName, PortUUID: portID, PortVersion: version, PortName: portName, Source: source}
	zero := 0
	_, err = o.nicCleanupTransact(ctx, root, nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan),
		ovsdb.Operation{
			Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero,
			Where: nicCleanupPortWhere(plan, true), Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: portID}}},
		}, root)
	if err != nil {
		return NICPortCleanup{}, err
	}

	return plan, nil
}

func nicCleanupPortWhere(plan NICPortCleanup, version bool) []ovsdb.Condition {
	where := []ovsdb.Condition{
		{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: plan.PortUUID}},
		{Column: "name", Function: ovsdb.ConditionEqual, Value: string(plan.PortName)},
		{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{ovnExtIDIncusLocation: plan.Source}}},
	}

	if version {
		where = append(where, ovsdb.Condition{Column: "_version", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: plan.PortVersion}})
	}

	return where
}

// ApplyNICPortCleanup changes only enabled on the original row. A replay may
// acknowledge that same source row already disabled, or its guarded absence;
// it never disables a replacement or a changed enabled generation.
func (o *NB) ApplyNICPortCleanup(ctx context.Context, plan NICPortCleanup) error {
	return o.checkNICPortCleanup(ctx, plan, true)
}

// VerifyNICPortCleanup checks stored source authority without changing any row.
// Same-source administrative disable and guarded original-row absence remain replayable.
func (o *NB) VerifyNICPortCleanup(ctx context.Context, plan NICPortCleanup) error {
	return o.checkNICPortCleanup(ctx, plan, false)
}

func (o *NB) checkNICPortCleanup(ctx context.Context, plan NICPortCleanup, apply bool) error {
	if plan.Version != 1 || plan.RootUUID != o.backendID || !nicCleanupUUID(plan.RootUUID) || !nicCleanupUUID(plan.SwitchUUID) || !nicCleanupUUID(plan.PortUUID) || !nicCleanupUUID(plan.PortVersion) || plan.SwitchName == "" || plan.PortName == "" || plan.Source == "" {
		return errors.New("Original NIC port cleanup identity is invalid or changed")
	}

	root := nicCleanupRootWait(plan.RootUUID)
	parent := nicCleanupPortParentWait(plan)
	id := ovsdb.UUID{GoUUID: plan.PortUUID}
	whereUUID := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}}
	results, err := o.nicCleanupTransact(ctx, root, parent,
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: whereUUID, Columns: []string{"_uuid", "_version", "name", "external_ids", "enabled"}})
	if err != nil {
		return err
	}

	zero := 0
	if len(results[2].Rows) == 0 {
		_, err = o.nicCleanupTransact(ctx, root, parent,
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: whereUUID, Columns: []string{"_uuid"}, Until: "!=", Rows: []ovsdb.Row{{"_uuid": id}}}, root)
		return err
	}

	if len(results[2].Rows) != 1 {
		return errors.New("Ambiguous original NIC cleanup port")
	}

	row := results[2].Rows[0]
	external, err := nicCleanupStringMap(row["external_ids"])
	if err != nil || row["name"] != string(plan.PortName) || external[ovnExtIDIncusLocation] != plan.Source {
		return errors.New("Original NIC port was renamed or transferred; retaining cleanup debt")
	}

	enabled, err := nicCleanupPortEnabled(row["enabled"])
	if err != nil {
		return err
	}

	var where []ovsdb.Condition
	if !enabled {
		// This is only an acknowledgment of administrative disable on the same
		// row/source. It is not proof of workload generation or any other effect.
		where = nicCleanupPortWhere(plan, false)
		where = append(where, ovsdb.Condition{Column: "enabled", Function: ovsdb.ConditionEqual, Value: ovsdb.OvsSet{GoSet: []any{false}}})
		_, err = o.nicCleanupTransact(ctx, root, parent, nicCleanupPortOwnerWait(plan),
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}}}, root)
		return err
	}

	// Row versions are server-local and OVN rewrites status columns such as "up" once
	// the source interface is detached. A new start generation of this row is excluded
	// by the pending cleanup debt (start publication refuses) and by the location guard,
	// so the original row is disabled by identity, only while it remains enabled.
	where = nicCleanupPortWhere(plan, false)
	where = append(where, ovsdb.Condition{Column: "enabled", Function: ovsdb.ConditionNotEqual, Value: ovsdb.OvsSet{GoSet: []any{false}}})

	if !apply {
		_, err = o.nicCleanupTransact(ctx, root, parent, nicCleanupPortOwnerWait(plan),
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}}}, root)
		return err
	}

	results, err = o.nicCleanupTransact(ctx, root, parent, nicCleanupPortOwnerWait(plan),
		ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id}}},
		ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: where, Row: ovsdb.Row{"enabled": ovsdb.OvsSet{GoSet: []any{false}}}}, root)
	if err != nil {
		return err
	}

	if results[4].Count != 1 {
		return fmt.Errorf("Original NIC port disable count is %d", results[4].Count)
	}

	return nil
}

// NICMigrationShared binds the exact finite shared-plane transition to a dispatch.
// Only location, chassis selection and prefix provenance change; all other rows remain guarded.
type NICMigrationShared struct {
	Version          int
	Operation        string
	TargetGeneration string
	Port             NICPortCleanup
	Target           string
	Owner            NICPrefixOwner
	Rows             []NICMigrationSharedRow
	Parents          []NICPortCleanup
}

// NICMigrationSharedRow records a captured original migration shared row.
type NICMigrationSharedRow struct {
	Table  string
	Before ovsdb.Row
	After  ovsdb.Row
}

func nicMigrationColumnValue(table, column string, value any) any {
	if table == "Address_Set" && column == "addresses" {
		values, err := nicCleanupStringSet(value)
		if err == nil {
			return nicCleanupStringSetWire(values)
		}
	}

	return value
}

func nicMigrationRowWait(table string, row ovsdb.Row, version bool) ovsdb.Operation {
	zero := 0
	where := []ovsdb.Condition{}
	keys := []string{}
	for key := range row {
		if key != "_version" || version {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)
	for _, key := range keys {
		where = append(where, ovsdb.Condition{Column: key, Function: ovsdb.ConditionEqual, Value: nicMigrationColumnValue(table, key, row[key])})
	}

	return ovsdb.Operation{Op: ovsdb.OperationWait, Table: table, Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": row["_uuid"]}}}
}

func nicMigrationValueEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}

	normalize := func(value any) []string {
		values := []any{value}
		{
			set, ok := value.(ovsdb.OvsSet)
			if ok {
				values = set.GoSet
			}
		}

		result := []string{}
		for _, value := range values {
			raw, _ := json.Marshal(value)
			result = append(result, string(raw))
		}

		slices.Sort(result)
		return result
	}

	_, aSet := a.(ovsdb.OvsSet)
	_, bSet := b.(ovsdb.OvsSet)
	if aSet || bSet {
		return slices.Equal(normalize(a), normalize(b))
	}

	return false
}

func (p NICMigrationShared) validate(root string) error {
	if p.Version != 1 || p.Port.RootUUID != root || !nicCleanupUUID(p.Operation) || !nicCleanupUUID(p.TargetGeneration) || p.Target == "" || p.Target == p.Port.Source || len(p.Rows) == 0 {
		return errors.New("Invalid migration shared transition identity")
	}

	if p.Owner.Generation != p.TargetGeneration || p.Owner.PortUUID != p.Port.PortUUID || p.Owner.RootUUID != p.Port.RootUUID || p.Owner.SwitchUUID != p.Port.SwitchUUID || p.Owner.PortName != p.Port.PortName || p.Owner.Source != p.Target {
		return errors.New("Migration prefix/target generation changed")
	}

	found := false
	seen := map[string]bool{}
	for _, row := range p.Rows {
		id, err := nicCleanupRowUUID(row.Before, "_uuid")
		if err != nil {
			return err
		}

		identity := row.Table + ":" + id
		if seen[identity] || row.After["_uuid"] != row.Before["_uuid"] {
			return errors.New("Migration shared row identity changed")
		}

		seen[identity] = true
		for key, value := range row.Before {
			if nicMigrationValueEqual(value, row.After[key]) {
				continue
			}

			permitted := key == "external_ids" && (row.Table == "Address_Set" || row.Table == "Logical_Switch_Port")
			permitted = permitted || row.Table == "Logical_Switch_Port" && key == "options"
			if !permitted {
				return fmt.Errorf("Migration may not alter guest/shared row %s.%s", row.Table, key)
			}
		}

		if len(row.Before) != len(row.After) {
			return errors.New("Migration changed shared row columns")
		}

		if row.Table == "Logical_Switch_Port" && id == p.Port.PortUUID {
			ids, err := nicCleanupStringMap(row.After["external_ids"])
			if err != nil {
				return err
			}

			if ids[ovnExtIDIncusLocation] != p.Target || ids[nicPrefixGeneration] != nicPrefixEncode(p.Owner) {
				return errors.New("Migration target publication marker changed")
			}

			old, err := nicCleanupStringMap(row.Before["external_ids"])
			if err != nil || old[ovnExtIDIncusLocation] != p.Port.Source {
				return errors.New("Migration original location changed")
			}

			referenceErr := validateTransferredNICConfig(p, row)
			if referenceErr != nil {
				return referenceErr
			}

			for key, value := range old {
				if key != ovnExtIDIncusLocation && key != nicPrefixGeneration && key != nicConfigPublicationKey && ids[key] != value {
					return errors.New("Migration discarded foreign port metadata")
				}
			}

			found = true
		}
	}

	if !found {
		return errors.New("Migration original shared port is missing")
	}

	return nil
}

func (p NICMigrationShared) operations(target bool) []ovsdb.Operation {
	operations := []ovsdb.Operation{nicCleanupRootWait(p.Port.RootUUID), nicCleanupPortParentWait(p.Port), nicCleanupPortOwnerWait(p.Port)}
	for _, parent := range p.Parents {
		operations = append(operations, nicCleanupPortParentWait(parent), nicCleanupPortOwnerWait(parent))
	}

	for _, row := range p.Rows {
		value := row.Before
		if !target {
			value = row.After
		}

		operations = append(operations, nicMigrationRowWait(row.Table, value, target))
	}

	for _, row := range p.Rows {
		from, to := row.Before, row.After
		if !target {
			from, to = to, from
		}

		changed := ovsdb.Row{}
		for key, value := range to {
			if !nicMigrationValueEqual(from[key], value) {
				changed[key] = value
			}
		}

		if len(changed) > 0 {
			operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: row.Table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: from["_uuid"]}}, Row: changed})
		}
	}

	return operations
}

// CaptureNICMigrationShared captures finite parents and all connected NB planes.
func (o *NB) CaptureNICMigrationShared(ctx context.Context, operation, target, generation, chassis string, port NICPortCleanup, router string, prefix OVNAddressSet, addresses []net.IPNet, proxySwitch OVNSwitch, proxyPort OVNSwitchPort, proxy []net.IPNet, retain ...string) (NICMigrationShared, error) {
	p := NICMigrationShared{Version: 1, Operation: operation, Target: target, TargetGeneration: generation, Port: port}
	if port.RootUUID != o.backendID || !nicCleanupUUID(operation) || !nicCleanupUUID(generation) || target == "" || target == port.Source || chassis == "" {
		return p, errors.New("Migration source/target root is incomplete")
	}

	owner, err := o.NewNICPrefixOwner(ctx, port.SwitchName, port.PortName, port.Source)
	if err != nil {
		return p, err
	}

	// The plan's rows are guarded by content; row versions are server-local and differ between relays
	// or after a database server restart, so only the port identity is compared here.
	if owner.PortUUID != port.PortUUID {
		return p, errors.New("Migration original port generation changed")
	}

	owner.Generation = generation
	publications, err := o.nicPrefixPublications(ctx, prefix, addresses, proxySwitch, proxyPort, proxy)
	if err != nil {
		return p, err
	}

	ops, err := o.nicPrefixPublishOperations(ctx, publications, owner, target, retain)
	if err != nil {
		return p, err
	}

	owner.Source = target
	p.Owner = owner
	if proxyPort != "" {
		identity, err := o.nicPrefixProxyIdentity(ctx, proxySwitch, proxyPort)
		if err != nil {
			return p, err
		}

		p.Parents = append(p.Parents, identity)
	}

	add := func(table string, row ovsdb.Row) {
		for _, existing := range p.Rows {
			if existing.Table == table && existing.Before["_uuid"] == row["_uuid"] {
				return
			}
		}

		p.Rows = append(p.Rows, NICMigrationSharedRow{Table: table, Before: maps.Clone(row), After: maps.Clone(row)})
	}

	read := func(table string, where []ovsdb.Condition) ([]ovsdb.Row, error) {
		results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: where})
		if err != nil {
			return nil, err
		}

		for _, row := range results[1].Rows {
			add(table, row)
		}

		return results[1].Rows, nil
	}

	byUUID := func(table, id string) (ovsdb.Row, error) {
		rows, err := read(table, []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}})
		if err != nil {
			return nil, err
		}

		if len(rows) != 1 {
			return nil, errors.New("Migration shared row is absent or ambiguous")
		}

		return rows[0], nil
	}

	portRow, err := byUUID("Logical_Switch_Port", port.PortUUID)
	if err != nil {
		return p, err
	}

	sw, err := byUUID("Logical_Switch", port.SwitchUUID)
	if err != nil {
		return p, err
	}
	// DNS records belong to the original switch; no name-only adoption is used.
	dns, err := nicCleanupUUIDSet(sw["dns_records"])
	if err != nil {
		return p, err
	}

	for _, id := range dns {
		_, err = byUUID("DNS", id)
		if err != nil {
			return p, err
		}
	}

	_, err = read("Port_Group", []ovsdb.Condition{{Column: "ports", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port.PortUUID}}}}})
	if err != nil {
		return p, err
	}

	if router != "" {
		r, err := byUUID("Logical_Router", router)
		if err != nil {
			return p, err
		}

		for column, table := range map[string]string{"nat": "NAT", "static_routes": "Logical_Router_Static_Route", "ports": "Logical_Router_Port"} {
			ids, err := nicCleanupUUIDSet(r[column])
			if err != nil {
				return p, err
			}

			for _, id := range ids {
				_, err = byUUID(table, id)
				if err != nil {
					return p, err
				}
			}
		}
	}

	for _, publication := range publications {
		add(publication.Table, publication.Row)
	}

	options, err := nicCleanupStringMap(portRow["options"])
	if err != nil {
		return p, err
	}

	options["requested-chassis"] = chassis
	for i := range ops {
		if ops[i].Op == ovsdb.OperationUpdate && ops[i].Table == "Logical_Switch_Port" && ops[i].Row["external_ids"] != nil {
			ids, e := nicCleanupStringMap(ops[i].Row["external_ids"])
			if e == nil && ids[nicPrefixGeneration] == nicPrefixEncode(owner) {
				ops[i].Row["options"] = nicCleanupStringMapWire(options)
			}
		}
	}

	for i := range p.Rows {
		row := &p.Rows[i]
		for _, op := range ops {
			if op.Op != ovsdb.OperationUpdate || op.Table != row.Table {
				continue
			}

			matched := false
			for _, cond := range op.Where {
				if cond.Column == "_uuid" && cond.Value == row.Before["_uuid"] {
					matched = true
				}
			}

			if matched {
				for key, value := range op.Row {
					row.After[key] = value
				}
			}
		}

		if row.Table == "Logical_Switch_Port" && row.Before["_uuid"] == portRow["_uuid"] {
			referenceErr := transferNICConfigPublication(&p, row)
			if referenceErr != nil {
				return p, referenceErr
			}
		}
	}

	err = p.validate(o.backendID)
	return p, err
}

// VerifyNICMigrationShared acknowledges exact transition fields without mutating them.
func (o *NB) VerifyNICMigrationShared(ctx context.Context, p NICMigrationShared, target bool) error {
	err := p.validate(o.backendID)
	if err != nil {
		return err
	}

	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(p.Port), nicCleanupPortOwnerWait(p.Port)}
	for _, parent := range p.Parents {
		operations = append(operations, nicCleanupPortParentWait(parent), nicCleanupPortOwnerWait(parent))
	}

	for _, row := range p.Rows {
		value := row.Before
		if target {
			value = row.After
		}

		operations = append(operations, nicMigrationRowWait(row.Table, value, false))
	}

	return o.nicPrefixCommit(ctx, operations)
}

// ApplyNICMigrationShared reconciles a lost reply against the exact target transition.
func (o *NB) ApplyNICMigrationShared(ctx context.Context, p NICMigrationShared) error {
	err := p.validate(o.backendID)
	if err != nil {
		return err
	}

	err = o.nicPrefixCommit(ctx, p.operations(true))
	if err != nil {
		ackErr := o.VerifyNICMigrationShared(ctx, p, true)
		if ackErr != nil {
			return errors.Join(err, ackErr)
		}
	}

	return o.VerifyNICMigrationShared(ctx, p, true)
}

// RollbackNICMigrationShared restores only this exact uncommitted transition.
func (o *NB) RollbackNICMigrationShared(ctx context.Context, p NICMigrationShared) error {
	err := p.validate(o.backendID)
	if err != nil {
		return err
	}

	if o.VerifyNICMigrationShared(ctx, p, false) == nil {
		return nil
	}

	operations := p.operations(false)
	err = o.nicPrefixCommit(ctx, operations)
	if err != nil {
		ackErr := o.VerifyNICMigrationShared(ctx, p, false)
		if ackErr != nil {
			return errors.Join(err, ackErr)
		}
	}

	return o.VerifyNICMigrationShared(ctx, p, false)
}

// VerifyNICMigrationTransferred follows the acknowledged target allocation.
// Mutable target rows need not remain frozen after their positive transfer receipt.
func (o *NB) VerifyNICMigrationTransferred(ctx context.Context, p NICMigrationShared) error {
	err := p.validate(o.backendID)
	if err != nil {
		return err
	}

	root, parent, owner := nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(p.Port), nicCleanupPortOwnerWait(p.Port)
	id := ovsdb.UUID{GoUUID: p.Port.PortUUID}
	where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}}
	results, err := o.nicCleanupTransact(ctx, root, parent, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: where})
	if err != nil {
		return err
	}

	zero := 0
	if len(results[2].Rows) == 0 {
		return o.nicPrefixCommit(ctx, []ovsdb.Operation{root, parent, {Op: ovsdb.OperationWait, Table: "Logical_Switch_Port", Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{}}})
	}

	if len(results[2].Rows) != 1 {
		return errors.New("Ambiguous transferred original port")
	}

	row := results[2].Rows[0]
	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return err
	}

	var current NICPrefixOwner
	err = json.Unmarshal([]byte(ids[nicPrefixGeneration]), &current)
	if err != nil || current.Validate(o.backendID, current.port()) != nil || current.PortUUID != p.Port.PortUUID || current.SwitchUUID != p.Port.SwitchUUID || current.PortName != p.Port.PortName || current.Source != ids[ovnExtIDIncusLocation] || row["name"] != string(p.Port.PortName) {
		return errors.New("Transferred original NIC allocation was repurposed")
	}

	var original ovsdb.Row
	for _, snapshot := range p.Rows {
		if snapshot.Table == "Logical_Switch_Port" && snapshot.Before["_uuid"] == id {
			original = snapshot.Before
		}
	}

	if original == nil || row["type"] != original["type"] {
		return errors.New("Transferred original NIC port type changed")
	}

	operations := []ovsdb.Operation{root, parent, owner, nicMigrationRowWait("Logical_Switch_Port", row, true)}
	if current == p.Owner {
		for _, key := range []string{"addresses", "port_security", "parent_name", "tag"} {
			if !nicMigrationValueEqual(row[key], original[key]) {
				return errors.New("Transferred NIC guest identity changed without an ownership transition")
			}
		}

		return o.nicPrefixCommit(ctx, operations)
	}
	// The existing finite prefix receipt proves retirement of this exact target owner.
	receipt := false
	for _, snapshot := range p.Rows {
		if snapshot.Table != "Address_Set" {
			continue
		}

		rows, err := o.nicPrefixRead(ctx, "Address_Set", "_uuid", snapshot.Before["_uuid"])
		if err != nil {
			return err
		}

		if len(rows) != 1 {
			return errors.New("Transferred prefix receipt parent is missing")
		}

		ledger, _, err := o.nicPrefixLedger("Address_Set", rows[0], false)
		if err != nil {
			return err
		}

		retired, ok := ledger.Released[p.TargetGeneration]
		if !ok || retired.Owner != p.Owner {
			return errors.New("Transferred target generation has no exact retirement receipt")
		}

		receipt = true
		operations = append(operations, nicPrefixRowWait("Address_Set", rows[0]))
	}

	if !receipt {
		return errors.New("Transferred target lineage is unavailable")
	}

	return o.nicPrefixCommit(ctx, operations)
}
