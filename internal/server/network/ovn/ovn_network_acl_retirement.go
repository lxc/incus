package ovn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NetworkACLRetirement identifies removed ACLs under the caller's durable normal update reservation.
type NetworkACLRetirement struct {
	ProjectID int64
	NetworkID int64
	ParentID  int64
	Token     string
	Root      string
	ACLIDs    []int64
}

func retirementNamedRow(s *physicalReferences, table, name string) (ovsdb.Row, error) {
	var found ovsdb.Row
	for _, row := range s.rows[table] {
		if row["name"] != name {
			continue
		}

		if found != nil {
			return nil, fmt.Errorf("Ambiguous network ACL retirement %s %q", table, name)
		}

		_, err := nicCleanupRowUUID(row, "_uuid")
		if err != nil {
			return nil, err
		}

		found = row
	}

	return found, nil
}

func retirementParent(s *physicalReferences, table, column string, child ovsdb.Row, expected ovsdb.Row) error {
	id, err := nicCleanupRowUUID(child, "_uuid")
	if err != nil {
		return err
	}

	parents := 0
	for _, row := range s.rows[table] {
		members, err := physicalUUIDs(row[column])
		if err != nil {
			return err
		}

		if !members[id] {
			continue
		}

		parents++
		if row["_uuid"] != expected["_uuid"] {
			return fmt.Errorf("Network ACL retirement port has a foreign/sibling parent")
		}
	}

	if parents != 1 {
		return fmt.Errorf("Network ACL retirement port lacks one exact parent")
	}

	return nil
}

// networkACLRetirementOperations permits only a positively resolved constructor router member.
func (s *physicalReferences) networkACLRetirementOperations(plan NetworkACLRetirement) ([]ovsdb.Operation, []int64, error) {
	if plan.ProjectID <= 0 || plan.NetworkID <= 0 || plan.ParentID < 0 || plan.ParentID == plan.NetworkID || !nicCleanupUUID(plan.Token) || plan.Root != s.root {
		return nil, nil, errors.New("Invalid rooted normal network ACL retirement plan")
	}

	switchName := fmt.Sprintf("incus-net%d-ls-int", plan.NetworkID)
	ownerID := plan.NetworkID
	if plan.ParentID != 0 {
		ownerID = plan.ParentID
	}

	routerName := fmt.Sprintf("incus-net%d-lr", ownerID)
	routerPort := routerName + "-lrp-int"
	if plan.ParentID != 0 {
		routerPort += fmt.Sprintf("-net%d", plan.NetworkID)
	}

	err := s.networkPortsConsistent(plan.NetworkID)
	if err != nil {
		return nil, nil, err
	}

	sw, err := retirementNamedRow(s, "Logical_Switch", switchName)
	if err != nil {
		return nil, nil, err
	}

	lsp, err := retirementNamedRow(s, "Logical_Switch_Port", switchName+"-lsp-router")
	if err != nil {
		return nil, nil, err
	}

	lrp, err := retirementNamedRow(s, "Logical_Router_Port", routerPort)
	if err != nil {
		return nil, nil, err
	}

	lr, err := retirementNamedRow(s, "Logical_Router", routerName)
	if err != nil {
		return nil, nil, err
	}

	if sw == nil || lsp == nil || lrp == nil || lr == nil || !s.networkInfrastructure(lsp, switchName, routerPort) {
		return nil, nil, fmt.Errorf("%w: network ACL retirement infrastructure identity changed", ErrPhysicalReference)
	}

	err = retirementParent(s, "Logical_Switch", "ports", lsp, sw)
	if err != nil {
		return nil, nil, err
	}

	err = retirementParent(s, "Logical_Router", "ports", lrp, lr)
	if err != nil {
		return nil, nil, err
	}

	portID, err := nicCleanupRowUUID(lsp, "_uuid")
	if err != nil {
		return nil, nil, err
	}

	ops := s.waits()
	retained := []int64{}
	seen := map[int64]bool{}
	for _, aclID := range plan.ACLIDs {
		if aclID <= 0 || seen[aclID] {
			return nil, nil, errors.New("Invalid/duplicate network ACL retirement identity")
		}

		seen[aclID] = true
		// Normal publications retain their original ACL even after membership was detached.
		retain := false
		allowed := map[string]bool{portID: true}
		for _, row := range s.rows["Logical_Switch_Port"] {
			ids, err := nicCleanupStringMap(row["external_ids"])
			if err != nil {
				return nil, nil, err
			}

			if ids[nicConfigPublicationKey] == "" {
				continue
			}

			p, err := decodeNICConfig(ids[nicConfigPublicationKey])
			if err != nil {
				return nil, nil, fmt.Errorf("%w: malformed pending NIC publication", ErrPhysicalReference)
			}

			err = p.valid(s.root)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: invalid pending NIC publication", ErrPhysicalReference)
			}

			for _, id := range p.ACLIDs {
				if id == aclID && p.NetworkID == plan.NetworkID {
					rowID, err := nicCleanupRowUUID(row, "_uuid")
					if err != nil {
						return nil, nil, err
					}

					switchID, err := nicCleanupRowUUID(sw, "_uuid")
					if err != nil {
						return nil, nil, err
					}

					if p.ProjectID != plan.ProjectID || p.PortUUID != rowID || p.SwitchUUID != switchID || ids[ovnExtIDIncusSwitch] != switchName {
						return nil, nil, fmt.Errorf("%w: retained ACL publication ownership changed", ErrPhysicalReference)
					}

					retain = true
					allowed[rowID] = true
				}
			}
		}

		name := fmt.Sprintf("incus_acl%d_net%d", aclID, plan.NetworkID)
		group, err := retirementNamedRow(s, "Port_Group", name)
		if err != nil {
			return nil, nil, err
		}

		if group == nil {
			if retain {
				retained = append(retained, aclID)
			}

			continue // Fresh guarded absence permits retry after an ambiguous earlier effect.
		}

		ids, err := nicCleanupStringMap(group["external_ids"])
		if err != nil {
			return nil, nil, err
		}

		associated := []string{}
		for _, suffix := range []string{"all", "ingress", "ingress_reversed", "egress", "egress_reversed"} {
			associated = append(associated, fmt.Sprintf("incus_acl%d_%s", aclID, suffix))
		}

		if ids[ovnExtIDIncusProjectID] != fmt.Sprint(plan.ProjectID) || ids[ovnExtIDIncusSwitch] != switchName || ids[ovnExtIDIncusPortGroup] != strings.Join(associated, ",") {
			return nil, nil, fmt.Errorf("%w: network ACL retirement group ownership changed", ErrPhysicalReference)
		}

		err = s.groupUnusedExcept(name, plan.ProjectID, allowed)
		if err != nil {
			return nil, nil, err
		}

		if retain {
			retained = append(retained, aclID)
			continue
		}

		id, err := nicCleanupRowUUID(group, "_uuid")
		if err != nil {
			return nil, nil, err
		}

		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Port_Group", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: id}}}})
	}

	return ops, retained, nil
}

// DeleteNetworkACLPortGroups preserves rooted publications and guards constructor-only deletions.
func (o *NB) DeleteNetworkACLPortGroups(ctx context.Context, plan NetworkACLRetirement) ([]int64, error) {
	if len(plan.ACLIDs) == 0 {
		return nil, nil
	}

	s, err := o.physicalReferenceSnapshot(ctx, "Logical_Router_Port")
	if err != nil {
		return nil, err
	}

	ops, retained, err := s.networkACLRetirementOperations(plan)
	if err != nil {
		return nil, err
	}

	_, err = o.nicCleanupTransact(ctx, ops...)
	if err != nil {
		return nil, err
	}

	return retained, nil
}
