package ovn

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// ErrPhysicalReference retains a resource still used by physical backend state.
var ErrPhysicalReference = errors.New("OVN resource retains physical references")

var physicalMatchReference = regexp.MustCompile(`[@$]([A-Za-z_][A-Za-z0-9_]*)`)

type physicalReferences struct {
	root    string
	rows    map[string][]ovsdb.Row
	tables  []string
	columns map[string][]string
	tunnels map[OVNSwitchPort]bool
}

func (o *NB) physicalReferenceSnapshot(ctx context.Context, extra ...string) (*physicalReferences, error) {
	if !nicCleanupUUID(o.backendID) {
		return nil, errors.New("Physical reference guard requires a fenced original NB root")
	}

	tables := append(slices.Clone(physicalGuardTables), extra...)
	ops := []ovsdb.Operation{nicCleanupRootWait(o.backendID)}
	columns := make(map[string][]string, len(tables))
	schema := o.client.Schema()
	for _, table := range tables {
		tableSchema := schema.Table(table)
		if tableSchema == nil {
			return nil, fmt.Errorf("Physical reference table %q is absent from the backend schema", table)
		}

		columns[table] = []string{"_uuid"}
		for column := range tableSchema.Columns {
			if column != "_uuid" && column != "_version" && !slices.Contains(physicalDerivedColumns[table], column) {
				columns[table] = append(columns[table], column)
			}
		}

		slices.Sort(columns[table])
		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{}, Columns: columns[table]})
	}

	result, err := o.nicCleanupTransact(ctx, ops...)
	if err != nil {
		return nil, err
	}

	snapshot := &physicalReferences{root: o.backendID, tables: tables, rows: map[string][]ovsdb.Row{}, columns: columns, tunnels: o.tunnelPorts}
	for i, table := range tables {
		snapshot.rows[table] = result[i+1].Rows
		for _, row := range snapshot.rows[table] {
			for _, column := range columns[table] {
				_, ok := row[column]
				if !ok {
					return nil, fmt.Errorf("Physical reference row in %q lacks schema column %q", table, column)
				}
			}
		}
	}

	return snapshot, nil
}

// physicalDerivedColumns are written by northd as ports bind and allocate; references never depend on
// them, and guarding them would abort unrelated writes whenever any port changes state.
var physicalDerivedColumns = map[string][]string{"Logical_Switch_Port": {"up", "dynamic_addresses"}}

// physicalGuardTables are the whole-table snapshots compared by reference guards.
var physicalGuardTables = []string{"Logical_Switch", "Logical_Switch_Port", "Port_Group", "ACL", "Address_Set", "Logical_Router_Policy", "Logical_Router"}

// PhysicalGuardMoved reports a definitive rejection by a whole-table reference guard wait.
// A failed wait aborts the OVSDB transaction before any effect; the root identity and
// fencing waits are never classified here, so the caller may only re-plan the operation.
func PhysicalGuardMoved(err error) bool {
	if errors.Is(err, backendDB.ErrFenced) {
		return false
	}

	var timedOut *ovsdb.TimedOut
	if !errors.As(err, &timedOut) || timedOut.Operation() == nil {
		return false
	}

	op := timedOut.Operation()
	if op.Op != ovsdb.OperationWait || op.Until != "==" || len(op.Where) != 1 || !slices.Contains(physicalGuardTables, op.Table) {
		return false
	}

	where := op.Where[0]
	value, ok := where.Value.(ovsdb.OvsMap)
	return ok && where.Column == "external_ids" && where.Function == ovsdb.ConditionIncludes && len(value.GoMap) == 0
}

// waits protects both existing rows and admission of a new dependency in the same effect transaction.
func (s *physicalReferences) waits() []ovsdb.Operation {
	ops := []ovsdb.Operation{nicCleanupRootWait(s.root)}
	zero := 0
	for _, table := range s.tables {
		rows := make([]ovsdb.Row, 0, len(s.rows[table]))
		for _, row := range s.rows[table] {
			rows = append(rows, maps.Clone(row))
		}

		// Row versions are server-local; compare complete content across relay forwarding.
		columns := s.columns[table]
		var sentinel ovsdb.UUID
		if len(rows) == 0 {
			// libovsdb omits empty wait.rows; assert membership with a transaction-local row.
			name := "physical_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			sentinel = ovsdb.UUID{GoUUID: name}
			row := ovsdb.Row{"name": name}
			switch table {
			case "DNS":
				row = ovsdb.Row{"external_ids": nicCleanupStringMapWire(map[string]string{"physical-sentinel": name})}
			case "QoS":
				row = ovsdb.Row{"direction": "from-lport", "match": "0", "priority": 0}
			case "ACL":
				row = ovsdb.Row{"action": "drop", "direction": "to-lport", "match": "0", "priority": 0}
			case "Logical_Router_Policy":
				row = ovsdb.Row{"action": "drop", "match": "0", "priority": 0}
			}

			ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: table, UUIDName: name, Row: row})
			columns = []string{"_uuid"}
			rows = []ovsdb.Row{{"_uuid": sentinel}}
		}
		// Every external_ids map includes the empty map, including rows with the zero UUID.
		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationWait, Table: table, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: nicCleanupStringMapWire(map[string]string{})}}, Columns: columns, Until: "==", Rows: rows, Timeout: &zero})
		if sentinel.GoUUID != "" {
			// Remove the exact named UUID before any effect, including on ambiguous replies.
			ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: sentinel}}})
		}
	}

	return ops
}

func physicalUUIDs(value any) (map[string]bool, error) {
	values, err := nicCleanupUUIDSet(value)
	if err != nil {
		return nil, err
	}

	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}

	return out, nil
}

func (s *physicalReferences) referenced(symbol byte, name string, ignoreACL map[string]bool) bool {
	for _, row := range append(append([]ovsdb.Row{}, s.rows["ACL"]...), s.rows["Logical_Router_Policy"]...) {
		id, _ := nicCleanupRowUUID(row, "_uuid")
		if ignoreACL[id] {
			continue
		}

		match, validMatch := row["match"].(string)
		if !validMatch {
			return true
		}

		for _, ref := range physicalMatchReference.FindAllString(match, -1) {
			if ref == string(symbol)+name {
				return true
			}
		}
	}

	return false
}

func (s *physicalReferences) groupUnused(name string, projectID int64) error {
	return s.groupUnusedExcept(name, projectID, nil)
}

func (s *physicalReferences) groupUnusedExcept(name string, projectID int64, allowed map[string]bool) error {
	for _, row := range s.rows["Port_Group"] {
		if row["name"] != name {
			continue
		}

		ids, err := nicCleanupStringMap(row["external_ids"])
		if err != nil || ids[ovnExtIDIncusProjectID] == "" || (projectID > 0 && ids[ovnExtIDIncusProjectID] != fmt.Sprint(projectID)) {
			return fmt.Errorf("%w: group %q has unknown/foreign project ownership", ErrPhysicalReference, name)
		}

		ports, err := physicalUUIDs(row["ports"])
		if err != nil {
			return err
		}

		for id := range ports {
			if allowed[id] {
				delete(ports, id)
			}
		}

		if len(ports) > 0 {
			return fmt.Errorf("%w: group %q still contains ports", ErrPhysicalReference, name)
		}

		ownACLs, err := physicalUUIDs(row["acls"])
		if err != nil {
			return err
		}

		for id := range ownACLs {
			for _, other := range s.rows["Port_Group"] {
				if other["_uuid"] == row["_uuid"] {
					continue
				}

				acls, err := physicalUUIDs(other["acls"])
				if err != nil {
					return err
				}

				if acls[id] {
					delete(ownACLs, id)
				}
			}

			for _, sw := range s.rows["Logical_Switch"] {
				acls, err := physicalUUIDs(sw["acls"])
				if err != nil {
					return err
				}

				if acls[id] {
					delete(ownACLs, id)
				}
			}
		}

		if s.referenced('@', name, ownACLs) {
			return fmt.Errorf("%w: group %q is an ACL subject", ErrPhysicalReference, name)
		}
	}

	return nil
}

func (s *physicalReferences) setUnused(prefix string) error {
	for _, suffix := range []string{"_ip4", "_ip6"} {
		if s.referenced('$', prefix+suffix, nil) {
			return fmt.Errorf("%w: address set %q is an ACL subject", ErrPhysicalReference, prefix+suffix)
		}
	}

	return nil
}

// CheckACLPhysicalUnused includes disabled ports and groups referenced only as rule subjects.
func (o *NB) CheckACLPhysicalUnused(ctx context.Context, projectID, aclID int64) error {
	if projectID <= 0 || aclID <= 0 {
		return errors.New("Invalid numeric ACL/project identity")
	}

	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}
	// A partial NIC update may already have detached membership while its original producer still owns rollback.
	for _, row := range s.rows["Logical_Switch_Port"] {
		ids, e := nicCleanupStringMap(row["external_ids"])
		if e != nil {
			return e
		}

		if ids[nicConfigPublicationKey] == "" {
			continue
		}

		p, e := decodeNICConfig(ids[nicConfigPublicationKey])
		if e != nil {
			return fmt.Errorf("%w: malformed NIC producer retains ambiguity", ErrPhysicalReference)
		}

		e = p.valid(s.root)
		if e != nil {
			return fmt.Errorf("%w: invalid NIC producer retains ambiguity", ErrPhysicalReference)
		}

		for _, id := range p.ACLIDs {
			if id == aclID {
				return fmt.Errorf("%w: NIC producer still retains original ACL %d", ErrPhysicalReference, aclID)
			}
		}
	}

	prefix := fmt.Sprintf("incus_acl%d", aclID)
	for _, row := range s.rows["Port_Group"] {
		name, validName := row["name"].(string)
		if !validName {
			return fmt.Errorf("%w: backend row has an invalid name", ErrPhysicalReference)
		}

		if name == prefix || strings.HasPrefix(name, prefix+"_") {
			err = s.groupUnused(name, projectID)
			if err != nil {
				return err
			}
		}
	}

	_, err = o.nicCleanupTransact(ctx, s.waits()...)
	return err
}

// CheckAddressSetPhysicalUnused refuses physical matches independently of Desired or tracking rows.
func (o *NB) CheckAddressSetPhysicalUnused(ctx context.Context, setID int64) error {
	if setID <= 0 {
		return errors.New("Invalid numeric address-set identity")
	}

	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}

	err = s.setUnused(fmt.Sprintf("incus_set%d", setID))
	if err != nil {
		return err
	}

	_, err = o.nicCleanupTransact(ctx, s.waits()...)
	return err
}

// networkPortsConsistent refuses orphaned or multiply parented rows claiming this network.
func (s *physicalReferences) networkPortsConsistent(networkID int64) error {
	name := fmt.Sprintf("incus-net%d-ls-int", networkID)
	prefix := fmt.Sprintf("incus-net%d-instance-", networkID)
	for _, row := range s.rows["Logical_Switch_Port"] {
		id, err := nicCleanupRowUUID(row, "_uuid")
		if err != nil {
			return err
		}

		ids, err := nicCleanupStringMap(row["external_ids"])
		if err != nil {
			return err
		}

		portName, validPortName := row["name"].(string)

		if !validPortName {
			return fmt.Errorf("%w: backend row has an invalid name", ErrPhysicalReference)
		}

		parents := 0
		ownParent := false
		for _, sw := range s.rows["Logical_Switch"] {
			ports, err := physicalUUIDs(sw["ports"])
			if err != nil {
				return err
			}

			if ports[id] {
				parents++
				if sw["name"] == name {
					ownParent = true
				}
			}
		}

		if ownParent || ids[ovnExtIDIncusSwitch] == name || strings.HasPrefix(portName, prefix) {
			if parents != 1 || !ownParent {
				return fmt.Errorf("%w: network port %q has unknown or sibling parent", ErrPhysicalReference, portName)
			}
		}
	}

	return nil
}

func (s *physicalReferences) networkInfrastructure(row ovsdb.Row, switchName, routerPort string) bool {
	ids, err := nicCleanupStringMap(row["external_ids"])
	opts, optErr := nicCleanupStringMap(row["options"])
	return err == nil && optErr == nil && row["name"] == switchName+"-lsp-router" && row["type"] == "router" && ids[ovnExtIDIncusSwitch] == switchName && opts["router-port"] == routerPort
}

// WithNetworkTunnelPorts returns a client whose shared reload checks accept these exact network tunnel ports.
func (o *NB) WithNetworkTunnelPorts(ports ...OVNSwitchPort) *NB {
	clone := *o
	clone.tunnelPorts = make(map[OVNSwitchPort]bool, len(ports))
	for _, port := range ports {
		clone.tunnelPorts[port] = true
	}

	return &clone
}

// physicalEmpty reports an unset optional column or empty set/map.
func physicalEmpty(value any) bool {
	switch v := value.(type) {
	case ovsdb.OvsSet:
		return len(v.GoSet) == 0
	case ovsdb.OvsMap:
		return len(v.GoMap) == 0
	}

	return false
}

// networkReloadInfrastructure also accepts the network's own configured tunnel ports, by exact
// name and by the exact row createOVNTunnel writes; any other port remains a NIC consumer.
func (s *physicalReferences) networkReloadInfrastructure(row ovsdb.Row, switchName, routerPort string) bool {
	if s.networkInfrastructure(row, switchName, routerPort) {
		return true
	}

	name, validName := row["name"].(string)
	if !validName || !s.tunnels[OVNSwitchPort(name)] {
		return false
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil || len(ids) != 1 || ids[ovnExtIDIncusSwitch] != switchName {
		return false
	}

	addresses := row["addresses"]
	set, isSet := addresses.(ovsdb.OvsSet)
	if isSet && len(set.GoSet) == 1 {
		addresses = set.GoSet[0]
	}

	if row["type"] != "" || addresses != "unknown" {
		return false
	}

	for _, column := range []string{"options", "parent_name", "tag_request", "dhcpv4_options", "dhcpv6_options", "port_security", "enabled"} {
		if !physicalEmpty(row[column]) {
			return false
		}
	}

	return true
}

// networkUnused permits only exact infrastructure ports, never a disabled or unknown consumer.
func (s *physicalReferences) networkUnused(networkID int64, routerPort string, peers ...NetworkPeerPolicy) error {
	if networkID <= 0 {
		return errors.New("Invalid numeric network identity")
	}

	referenceErr := s.networkPortsConsistent(networkID)
	if referenceErr != nil {
		return referenceErr
	}

	prefix := fmt.Sprintf("incus-net%d", networkID)
	switchName := prefix + "-ls-int"
	infrastructure := map[string]bool{}
	for _, sw := range s.rows["Logical_Switch"] {
		name, validName := sw["name"].(string)
		if !validName {
			return fmt.Errorf("%w: backend row has an invalid name", ErrPhysicalReference)
		}

		if name != switchName {
			continue
		}

		ports, err := physicalUUIDs(sw["ports"])
		if err != nil {
			return err
		}

		for _, port := range s.rows["Logical_Switch_Port"] {
			id, _ := nicCleanupRowUUID(port, "_uuid")
			if !ports[id] {
				continue
			}

			// The network's own configured tunnel ports are removed with it.
			if s.networkReloadInfrastructure(port, switchName, routerPort) {
				infrastructure[id] = true
				continue
			}

			ids, err := nicCleanupStringMap(port["external_ids"])
			opts, optErr := nicCleanupStringMap(port["options"])
			if err != nil || optErr != nil || port["name"] != switchName+"-lsp-router" || port["type"] != "router" || ids[ovnExtIDIncusSwitch] != name || opts["router-port"] != routerPort {
				return fmt.Errorf("%w: network %d retains port %q", ErrPhysicalReference, networkID, port["name"])
			}

			infrastructure[id] = true
		}
	}

	for _, row := range s.rows["Port_Group"] {
		name, validName := row["name"].(string)
		if !validName {
			return fmt.Errorf("%w: backend row has an invalid name", ErrPhysicalReference)
		}

		ids, _ := nicCleanupStringMap(row["external_ids"])
		if ids[ovnExtIDIncusSwitch] == switchName || name == fmt.Sprintf("incus_net%d", networkID) {
			err := s.groupUnusedExcept(name, 0, infrastructure)
			if err != nil {
				return err
			}
		}
	}

	ownPolicies := map[string]bool{}
	for _, router := range s.rows["Logical_Router"] {
		routerName, validRouterName := router["name"].(string)
		if !validRouterName {
			return fmt.Errorf("%w: backend router has an invalid name", ErrPhysicalReference)
		}

		if !strings.HasPrefix(routerPort, routerName+"-lrp-int") {
			continue
		}

		policies, err := physicalUUIDs(router["policies"])
		if err != nil {
			return err
		}

		for _, policy := range s.rows["Logical_Router_Policy"] {
			id, _ := nicCleanupRowUUID(policy, "_uuid")
			match, validMatch := policy["match"].(string)
			if !validMatch {
				return fmt.Errorf("%w: backend row has an invalid match", ErrPhysicalReference)
			}

			if policies[id] && strings.Contains(match, fmt.Sprintf(`inport == "%s"`, routerPort)) {
				ownPolicies[id] = true
			}
		}
	}

	peerPolicies, err := s.networkPeerPolicies(networkID, peers)
	if err != nil {
		return err
	}

	for id := range peerPolicies {
		ownPolicies[id] = true
	}

	for _, suffix := range []string{"_ip4", "_ip6"} {
		if s.referenced('$', fmt.Sprintf("incus_net%d_routes%s", networkID, suffix), ownPolicies) {
			return fmt.Errorf("%w: network route set has an external physical rule", ErrPhysicalReference)
		}
	}

	return nil
}

// CheckNetworkPhysicalUnused runs before any remote notification or local teardown.
func (o *NB) CheckNetworkPhysicalUnused(ctx context.Context, networkID int64, routerPort string, peers ...NetworkPeerPolicy) error {
	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}

	err = s.networkUnused(networkID, routerPort, peers...)
	if err != nil {
		return err
	}

	_, err = o.nicCleanupTransact(ctx, s.waits()...)
	return err
}

func (o *NB) deletePhysicalGroups(ctx context.Context, names []OVNPortGroup) error {
	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}

	ops := s.waits()
	for _, name := range names {
		err = s.groupUnused(string(name), 0)
		if errors.Is(err, ErrPhysicalReference) {
			continue
		}

		if err != nil {
			return err
		}

		for _, row := range s.rows["Port_Group"] {
			if row["name"] == string(name) {
				ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}})
			}
		}
	}

	_, err = o.nicCleanupTransact(ctx, ops...)
	return err
}

func (o *NB) deletePhysicalSet(ctx context.Context, prefix OVNAddressSet) error {
	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return err
	}

	err = s.setUnused(string(prefix))
	if errors.Is(err, ErrPhysicalReference) {
		return nil
	}

	if err != nil {
		return err
	}

	ops := s.waits()
	for _, row := range s.rows["Address_Set"] {
		if row["name"] == string(prefix)+"_ip4" || row["name"] == string(prefix)+"_ip6" {
			ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Address_Set", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}})
		}
	}

	_, err = o.nicCleanupTransact(ctx, ops...)
	return err
}

// switchDeleteWaits protects the exact internal switch reference observations at deletion.
func (o *NB) switchDeleteWaits(ctx context.Context, name OVNSwitch) ([]ovsdb.Operation, error) {
	if !strings.HasPrefix(string(name), "incus-net") || !strings.HasSuffix(string(name), "-ls-int") {
		return nil, nil
	}

	var networkID int64
	_, err := fmt.Sscanf(string(name), "incus-net%d-ls-int", &networkID)
	if err != nil || string(name) != fmt.Sprintf("incus-net%d-ls-int", networkID) {
		return nil, errors.New("Invalid numeric internal switch identity")
	}

	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	routerPort := ""
	for _, row := range s.rows["Logical_Switch_Port"] {
		if row["name"] == string(name)+"-lsp-router" {
			options, err := nicCleanupStringMap(row["options"])
			if err != nil {
				return nil, err
			}

			routerPort = options["router-port"]
		}
	}

	err = s.networkUnused(networkID, routerPort)
	if err != nil {
		return nil, err
	}

	return s.waits(), nil
}
