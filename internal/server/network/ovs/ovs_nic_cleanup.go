package ovs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICPortCleanup binds OVS detach to original source rows, independently of
// current host names. It does not certify kernel cleanup or full NIC teardown.
type NICPortCleanup struct {
	Version       int
	RootUUID      string
	BridgeUUID    string
	BridgeName    string
	PortUUID      string
	InterfaceUUID string
	InterfaceName string
	OVNPortName   string

	// Absent records a rooted proof that no interface on the bridge was bound to
	// OVNPortName. It carries no port/interface rows and never deletes anything.
	Absent bool
}

// Validate rejects incomplete or malformed original OVS row identities.
func (p NICPortCleanup) Validate() error {
	if p.Absent {
		if p.Version != 1 || p.BridgeName == "" || p.OVNPortName == "" || p.InterfaceName != "" || p.PortUUID != "" || p.InterfaceUUID != "" {
			return errors.New("Original OVS NIC absence identity is incomplete")
		}

		for _, value := range []string{p.RootUUID, p.BridgeUUID} {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil || id.String() != value {
				return errors.New("Original OVS NIC absence row UUID is invalid")
			}
		}

		return nil
	}

	if p.Version != 1 || p.BridgeName == "" || p.InterfaceName == "" || p.OVNPortName == "" {
		return errors.New("Original OVS NIC cleanup identity is incomplete")
	}

	for _, value := range []string{p.RootUUID, p.BridgeUUID, p.PortUUID, p.InterfaceUUID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("Original OVS NIC cleanup row UUID is invalid")
		}
	}

	return nil
}

func nicPortRootWait(root string) ovsdb.Operation {
	zero := 0
	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "Open_vSwitch", Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{}}}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: root}}},
	}
}

func nicPortUUIDWhere(id string) []ovsdb.Condition {
	return []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}
}

func nicPortRowWait(table string, where []ovsdb.Condition, id string) ovsdb.Operation {
	zero := 0
	rows := []ovsdb.Row{}
	if id != "" {
		rows = append(rows, ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: id}})
	}

	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: table, Timeout: &zero,
		Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: rows,
	}
}

func nicPortSet(id string) ovsdb.OvsSet {
	return ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: id}}}
}

// checkNICPortAbsence proves, in one read-only transaction, the rooted bridge and
// that no interface is bound to the OVN port. An empty wait cannot be sent on the wire.
func (o *VSwitch) checkNICPortAbsence(ctx context.Context, p NICPortCleanup) error {
	rootOwnsBridge := append(nicPortUUIDWhere(p.RootUUID), ovsdb.Condition{
		Column: "bridges", Function: ovsdb.ConditionIncludes, Value: nicPortSet(p.BridgeUUID),
	})
	bridge := append(nicPortUUIDWhere(p.BridgeUUID), ovsdb.Condition{
		Column: "name", Function: ovsdb.ConditionEqual, Value: p.BridgeName,
	})
	bound := []ovsdb.Condition{{
		Column: "external_ids", Function: ovsdb.ConditionIncludes,
		Value: ovsdb.OvsMap{GoMap: map[any]any{"iface-id": p.OVNPortName}},
	}}
	results, err := o.nicPortCleanupTransact(ctx,
		nicPortRootWait(p.RootUUID),
		nicPortRowWait("Open_vSwitch", rootOwnsBridge, p.RootUUID),
		nicPortRowWait("Bridge", bridge, p.BridgeUUID),
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Interface", Where: bound, Columns: []string{"_uuid"}},
		nicPortRootWait(p.RootUUID))
	if err != nil {
		return err
	}

	if len(results[3].Rows) != 0 {
		return errors.New("Original OVS NIC is still associated")
	}

	return nil
}

func nicPortCleanupGuards(p NICPortCleanup, rowsPresent bool) []ovsdb.Operation {
	rootOwnsBridge := append(nicPortUUIDWhere(p.RootUUID), ovsdb.Condition{
		Column: "bridges", Function: ovsdb.ConditionIncludes, Value: nicPortSet(p.BridgeUUID),
	})
	bridge := append(nicPortUUIDWhere(p.BridgeUUID), ovsdb.Condition{
		Column: "name", Function: ovsdb.ConditionEqual, Value: p.BridgeName,
	})
	guards := []ovsdb.Operation{
		nicPortRootWait(p.RootUUID),
		nicPortRowWait("Open_vSwitch", rootOwnsBridge, p.RootUUID),
		nicPortRowWait("Bridge", bridge, p.BridgeUUID),
	}

	if !rowsPresent {
		// A unique immutable UUID absence uses nonempty rows on the wire.
		port := nicPortRowWait("Port", nicPortUUIDWhere(p.PortUUID), p.PortUUID)
		port.Until = "!="
		iface := nicPortRowWait("Interface", nicPortUUIDWhere(p.InterfaceUUID), p.InterfaceUUID)
		iface.Until = "!="
		return append(guards, port, iface)
	}

	port := append(nicPortUUIDWhere(p.PortUUID),
		ovsdb.Condition{Column: "name", Function: ovsdb.ConditionEqual, Value: p.InterfaceName},
		ovsdb.Condition{Column: "interfaces", Function: ovsdb.ConditionEqual, Value: nicPortSet(p.InterfaceUUID)})
	iface := append(nicPortUUIDWhere(p.InterfaceUUID),
		ovsdb.Condition{Column: "name", Function: ovsdb.ConditionEqual, Value: p.InterfaceName},
		ovsdb.Condition{
			Column: "external_ids", Function: ovsdb.ConditionIncludes,
			Value: ovsdb.OvsMap{GoMap: map[any]any{"iface-id": p.OVNPortName}},
		})
	return append(guards,
		nicPortRowWait("Bridge", []ovsdb.Condition{{Column: "ports", Function: ovsdb.ConditionIncludes, Value: nicPortSet(p.PortUUID)}}, p.BridgeUUID),
		nicPortRowWait("Port", []ovsdb.Condition{{Column: "interfaces", Function: ovsdb.ConditionIncludes, Value: nicPortSet(p.InterfaceUUID)}}, p.PortUUID),
		nicPortRowWait("Port", port, p.PortUUID),
		nicPortRowWait("Interface", iface, p.InterfaceUUID))
}

func (o *VSwitch) nicPortCleanupTransact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	if ctx == nil || o.client == nil {
		return nil, errors.New("Original OVS NIC cleanup requires context and client")
	}

	results, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return nil, err
	}

	if len(results) != len(operations) {
		return nil, errors.New("Original OVS NIC cleanup response is incomplete")
	}

	_, err = ovsdb.CheckOperationResults(results, operations)
	if err != nil {
		return nil, err
	}

	return results, nil
}

func nicPortRowUUID(row ovsdb.Row) (string, error) {
	id, ok := row["_uuid"].(ovsdb.UUID)
	if !ok {
		return "", errors.New("Original OVS NIC row UUID is missing")
	}

	parsed, err := uuid.Parse(id.GoUUID)
	if err != nil || parsed == uuid.Nil || parsed.String() != id.GoUUID {
		return "", errors.New("Original OVS NIC row UUID is invalid")
	}

	return id.GoUUID, nil
}

// CaptureNICPortCleanup captures only a rooted, uniquely owned single-interface
// port with the expected OVN binding. Both RPCs are read-only guarded operations.
func (o *VSwitch) CaptureNICPortCleanup(ctx context.Context, bridgeName string, interfaceName string, ovnPortName string) (NICPortCleanup, error) {
	if o.backendID == "" || bridgeName == "" || interfaceName == "" || ovnPortName == "" {
		return NICPortCleanup{}, errors.New("Original OVS NIC source/database identity is missing")
	}

	nameWhere := func(name string) []ovsdb.Condition {
		return []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: name}}
	}

	results, err := o.nicPortCleanupTransact(ctx, nicPortRootWait(o.backendID),
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Bridge", Where: nameWhere(bridgeName), Columns: []string{"_uuid"}},
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port", Where: nameWhere(interfaceName), Columns: []string{"_uuid"}},
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Interface", Where: nameWhere(interfaceName), Columns: []string{"_uuid"}})
	if err != nil {
		return NICPortCleanup{}, err
	}

	for _, index := range []int{1, 2, 3} {
		if len(results[index].Rows) != 1 {
			return NICPortCleanup{}, errors.New("Original OVS NIC bridge/port/interface is absent or ambiguous")
		}
	}

	bridge, err := nicPortRowUUID(results[1].Rows[0])
	if err != nil {
		return NICPortCleanup{}, err
	}

	port, err := nicPortRowUUID(results[2].Rows[0])
	if err != nil {
		return NICPortCleanup{}, err
	}

	iface, err := nicPortRowUUID(results[3].Rows[0])
	if err != nil {
		return NICPortCleanup{}, err
	}

	plan := NICPortCleanup{
		Version: 1, RootUUID: o.backendID, BridgeUUID: bridge, BridgeName: bridgeName,
		PortUUID: port, InterfaceUUID: iface, InterfaceName: interfaceName, OVNPortName: ovnPortName,
	}

	err = plan.Validate()
	if err != nil {
		return NICPortCleanup{}, err
	}

	_, err = o.nicPortCleanupTransact(ctx, append(nicPortCleanupGuards(plan, true), nicPortRootWait(plan.RootUUID))...)
	if err != nil {
		return NICPortCleanup{}, err
	}

	return plan, nil
}

// CaptureNICPortAbsence captures a rooted proof that no OVS interface is bound
// to the OVN port. It is used only once the original host link is positively gone.
func (o *VSwitch) CaptureNICPortAbsence(ctx context.Context, bridgeName string, ovnPortName string) (NICPortCleanup, error) {
	if o.backendID == "" || bridgeName == "" || ovnPortName == "" {
		return NICPortCleanup{}, errors.New("Original OVS NIC source/database identity is missing")
	}

	results, err := o.nicPortCleanupTransact(ctx, nicPortRootWait(o.backendID),
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Bridge", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: bridgeName}}, Columns: []string{"_uuid"}})
	if err != nil {
		return NICPortCleanup{}, err
	}

	if len(results[1].Rows) != 1 {
		return NICPortCleanup{}, errors.New("Original OVS NIC bridge is absent or ambiguous")
	}

	bridge, err := nicPortRowUUID(results[1].Rows[0])
	if err != nil {
		return NICPortCleanup{}, err
	}

	plan := NICPortCleanup{Version: 1, RootUUID: o.backendID, BridgeUUID: bridge, BridgeName: bridgeName, OVNPortName: ovnPortName, Absent: true}
	err = plan.Validate()
	if err != nil {
		return NICPortCleanup{}, err
	}

	err = o.checkNICPortAbsence(ctx, plan)
	if err != nil {
		return NICPortCleanup{}, fmt.Errorf("Failed proving original OVS NIC absence: %w", err)
	}

	return plan, nil
}

// ApplyNICPortCleanup detaches only the captured UUIDs and acknowledges their
// guarded absence on replay. A same-name replacement is never selected for deletion.
// Dynamic statistics do not change ownership; the UUID/name/parents/iface-id
// guards remain in the atomic write transaction.
func (o *VSwitch) ApplyNICPortCleanup(ctx context.Context, plan NICPortCleanup) error {
	err := plan.Validate()
	if err != nil || plan.RootUUID != o.backendID {
		return errors.New("Original OVS NIC cleanup database/row identity changed")
	}

	if plan.Absent {
		// Nothing was attached; acknowledge only a still-guarded absence.
		return o.checkNICPortAbsence(ctx, plan)
	}

	root := nicPortRootWait(plan.RootUUID)
	results, err := o.nicPortCleanupTransact(ctx, root,
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port", Where: nicPortUUIDWhere(plan.PortUUID), Columns: []string{"_uuid"}},
		ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Interface", Where: nicPortUUIDWhere(plan.InterfaceUUID), Columns: []string{"_uuid"}})
	if err != nil {
		return err
	}

	if len(results[1].Rows) == 0 && len(results[2].Rows) == 0 {
		_, err = o.nicPortCleanupTransact(ctx, append(nicPortCleanupGuards(plan, false), root)...)
		return err
	}

	if len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		return errors.New("Original OVS NIC cleanup has inconsistent remaining rows")
	}

	operations := nicPortCleanupGuards(plan, true)
	firstWrite := len(operations)
	operations = append(operations,
		ovsdb.Operation{
			Op: ovsdb.OperationMutate, Table: "Bridge", Where: nicPortUUIDWhere(plan.BridgeUUID),
			Mutations: []ovsdb.Mutation{{Column: "ports", Mutator: ovsdb.MutateOperationDelete, Value: nicPortSet(plan.PortUUID)}},
		},
		ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port", Where: nicPortUUIDWhere(plan.PortUUID)},
		ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Interface", Where: nicPortUUIDWhere(plan.InterfaceUUID)}, root)
	results, err = o.nicPortCleanupTransact(ctx, operations...)
	if err != nil {
		return err
	}

	for _, index := range []int{firstWrite, firstWrite + 1, firstWrite + 2} {
		if results[index].Count != 1 {
			return fmt.Errorf("Original OVS NIC cleanup write %d was not acknowledged", index)
		}
	}

	return nil
}
