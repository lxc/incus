package ovn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// ErrNICRetirementAbsent reports that the switch port to retire does not exist.
var ErrNICRetirementAbsent = errors.New("Original retirement switch/port is absent; retain the enclosing update reservation")

// RetireNICConfig removes the exact stopped producer and its private config in one rooted transaction.
func (o *NB) RetireNICConfig(ctx context.Context, swName OVNSwitch, portName OVNSwitchPort, target NICConfigPublication, releaseDHCP bool) error {
	s, err := o.physicalReferenceSnapshot(ctx, "DNS", "QoS")
	if err != nil {
		return err
	}

	var sw, port ovsdb.Row
	for _, row := range s.rows["Logical_Switch"] {
		if row["name"] == string(swName) {
			if sw != nil {
				return errors.New("Ambiguous retirement switch")
			}

			sw = row
		}
	}

	for _, row := range s.rows["Logical_Switch_Port"] {
		if row["name"] == string(portName) {
			if port != nil {
				return errors.New("Ambiguous retirement port")
			}

			port = row
		}
	}

	if sw == nil || port == nil {
		return ErrNICRetirementAbsent
	}

	_, err = s.nicConfig(port, sw, target, false)
	if err != nil {
		return err
	}

	enabled, err := nicCleanupPortEnabled(port["enabled"])
	if err != nil {
		return err
	}

	if enabled {
		return errors.New("Original NIC must complete Stop before physical retirement")
	}

	id, err := nicCleanupRowUUID(port, "_uuid")
	if err != nil {
		return err
	}

	sid, err := nicCleanupRowUUID(sw, "_uuid")
	if err != nil {
		return err
	}

	for _, other := range s.rows["Logical_Switch"] {
		ports, err := physicalUUIDs(other["ports"])
		if err != nil {
			return err
		}

		if ports[id] && other["_uuid"] != sw["_uuid"] {
			return errors.New("Retirement port has a foreign switch parent")
		}
	}

	ops := s.waits()
	mutate := func(table string, row ovsdb.Row, column string, values map[string]bool) {
		wire := []any{}
		for value := range values {
			wire = append(wire, ovsdb.UUID{GoUUID: value})
		}

		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Mutations: []ovsdb.Mutation{{Column: column, Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: wire}}}})
	}

	for _, group := range s.rows["Port_Group"] {
		ports, err := physicalUUIDs(group["ports"])
		if err != nil {
			return err
		}

		if ports[id] {
			mutate("Port_Group", group, "ports", map[string]bool{id: true})
		}
	}

	for _, table := range []string{"ACL", "QoS", "DNS"} {
		column := "acls"
		parentTable := "Port_Group"
		if table == "QoS" {
			column = "qos_rules"
			parentTable = "Logical_Switch"
		}

		if table == "DNS" {
			column = "dns_records"
			parentTable = "Logical_Switch"
		}

		for _, row := range s.rows[table] {
			ids, err := nicCleanupStringMap(row["external_ids"])
			if err != nil {
				return err
			}

			if ids[ovnExtIDIncusSwitchPort] != string(portName) {
				continue
			}

			rid, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return err
			}

			allowedName := string(swName)
			if table == "ACL" {
				allowedName = fmt.Sprintf("incus_net%d", target.NetworkID)
			}

			parents := 0
			for _, parent := range s.rows[parentTable] {
				rows, err := physicalUUIDs(parent[column])
				if err != nil {
					return err
				}

				if !rows[rid] {
					continue
				}

				if parent["name"] != allowedName {
					return fmt.Errorf("Retirement %s row has a sibling/foreign parent", table)
				}

				parents++
				mutate(parentTable, parent, column, map[string]bool{rid: true})
			}

			if table == "ACL" {
				for _, other := range s.rows["Logical_Switch"] {
					rows, err := physicalUUIDs(other["acls"])
					if err != nil {
						return err
					}

					if rows[rid] {
						return errors.New("Retirement ACL has a foreign logical-switch parent")
					}
				}
			}

			// A port-private DNS row is a root row; a detached one has no parent and is still private.
			if parents > 1 || (parents == 0 && table != "DNS") {
				return fmt.Errorf("Retirement %s private ownership is ambiguous", table)
			}

			ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}})
		}
	}

	if releaseDHCP && target.Input["ipv4.address"] != "" {
		other, err := nicCleanupStringMap(sw["other_config"])
		if err != nil {
			return err
		}

		excluded := []string{}
		wanted := net.ParseIP(target.Input["ipv4.address"])
		for _, entry := range strings.Fields(other["exclude_ips"]) {
			parsed := net.ParseIP(entry)
			if wanted != nil && parsed != nil && wanted.Equal(parsed) {
				continue
			}

			excluded = append(excluded, entry)
		}

		if len(excluded) == 0 {
			delete(other, "exclude_ips")
		} else {
			other["exclude_ips"] = strings.Join(excluded, " ")
		}

		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: sid}}}, Row: ovsdb.Row{"other_config": nicCleanupStringMapWire(other)}})
	}

	mutate("Logical_Switch", sw, "ports", map[string]bool{id: true})
	ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: port["_uuid"]}}})
	_, err = o.nicCleanupTransact(ctx, ops...)
	// A failed or ambiguous response is kept visible; the enclosing instance rollback owns recovery.
	return err
}
