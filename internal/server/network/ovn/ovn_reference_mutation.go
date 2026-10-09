package ovn

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// referenceMutationClient belongs to one bounded caller, never to the shared state client.
type referenceMutationClient struct {
	ovsdbClient.Client
	base   *NB
	check  func(*physicalReferences) error
	replay map[OVNSwitchPort]NICReplayProducer
}

// Transact rechecks the caller reference reservation before each backend effect.
func (c *referenceMutationClient) Transact(ctx context.Context, effects ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	mutation := false
	for _, op := range effects {
		if op.Op == ovsdb.OperationInsert || op.Op == ovsdb.OperationUpdate || op.Op == ovsdb.OperationMutate || op.Op == ovsdb.OperationDelete {
			mutation = true
			break
		}
	}

	if !mutation {
		return c.Client.Transact(ctx, effects...)
	}

	s, err := c.base.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	err = c.check(s)
	if err != nil {
		return nil, err
	}

	guards := s.waits()
	ops := append(guards, effects...)
	result, err := c.Client.Transact(ctx, ops...)
	if err != nil {
		return nil, err
	}

	if len(result) < len(guards) {
		return nil, errors.New("Physical reference guard response is incomplete")
	}

	_, err = ovsdb.CheckOperationResults(result[:len(guards)], guards)
	if err != nil {
		return nil, err
	}
	// Preserve effect indexes and any transaction-wide failure for the original caller.
	return result[len(guards):], nil
}

func (o *NB) withReferenceMutation(check func(*physicalReferences) error) *NB {
	clone := *o
	clone.client = &referenceMutationClient{Client: o.client, base: o, check: check}
	return &clone
}

// GuardNetworkNICReplay pins actual producer identities through every shared reload mutation.
func (o *NB) GuardNetworkNICReplay(ctx context.Context, networkID int64, routerPort string, targets, planned map[OVNSwitchPort]NICConfigPublication, selected ...map[OVNSwitchPort]NICReplayProducer) (*NB, error) {
	snapshot, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	referenceErr := snapshot.networkPortsConsistent(networkID)
	if referenceErr != nil {
		return nil, referenceErr
	}

	originals := map[OVNSwitchPort]NICReplayProducer{}
	name := fmt.Sprintf("incus-net%d-ls-int", networkID)
	for _, sw := range snapshot.rows["Logical_Switch"] {
		if sw["name"] != name {
			continue
		}

		ports, err := physicalUUIDs(sw["ports"])
		if err != nil {
			return nil, err
		}

		for _, row := range snapshot.rows["Logical_Switch_Port"] {
			id, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return nil, err
			}

			if !ports[id] {
				continue
			}

			portName, validPortName := row["name"].(string)
			if !validPortName {
				return nil, errors.New("Physical reload consumer has an invalid name")
			}

			if snapshot.networkReloadInfrastructure(row, name, routerPort) {
				continue
			}

			target, ok := targets[OVNSwitchPort(portName)]
			if !ok {
				return nil, errors.New("Physical reload consumer has no selected candidate")
			}

			p, err := snapshot.nicReplayProducer(row, sw, target)
			if err != nil {
				return nil, err
			}

			if len(selected) > 0 && !reflect.DeepEqual(selected[0][OVNSwitchPort(portName)], p) {
				return nil, errors.New("Shared reload selected producer changed before admission")
			}

			originals[OVNSwitchPort(portName)] = p
		}
	}

	if len(selected) > 0 && len(selected[0]) != len(originals) {
		return nil, errors.New("Shared reload selected consumer disappeared before admission")
	}

	guarded := o.withReferenceMutation(func(s *physicalReferences) error {
		referenceErr := s.networkPortsConsistent(networkID)
		if referenceErr != nil {
			return referenceErr
		}

		seen := map[OVNSwitchPort]bool{}
		for _, sw := range s.rows["Logical_Switch"] {
			if sw["name"] != name {
				continue
			}

			ports, err := physicalUUIDs(sw["ports"])
			if err != nil {
				return err
			}

			for _, row := range s.rows["Logical_Switch_Port"] {
				id, err := nicCleanupRowUUID(row, "_uuid")
				if err != nil {
					return err
				}

				if !ports[id] {
					continue
				}

				portName, validPortName := row["name"].(string)
				if !validPortName {
					return errors.New("Physical reload consumer has an invalid name")
				}

				if s.networkReloadInfrastructure(row, name, routerPort) {
					continue
				}

				original, ok := originals[OVNSwitchPort(portName)]
				if !ok {
					return errors.New("New physical consumer entered shared reload")
				}

				current, err := s.nicConfigIdentity(row, sw, original, planned[OVNSwitchPort(portName)])
				if err != nil {
					return err
				}

				if current.PortUUID != original.Publication.PortUUID || current.SwitchUUID != original.Publication.SwitchUUID || current.Generation != original.Publication.Generation {
					return errors.New("Shared reload original producer was replaced")
				}

				seen[OVNSwitchPort(portName)] = true
			}
		}

		if len(seen) != len(originals) {
			return errors.New("Shared reload original consumer disappeared")
		}

		return nil
	})
	guarded.client.(*referenceMutationClient).replay = originals
	return guarded, nil
}

func (s *physicalReferences) nicConfigIdentity(row, sw ovsdb.Row, original NICReplayProducer, planned NICConfigPublication) (NICConfigPublication, error) {
	target := original.Publication
	// Only this pinned enclosing caller may continue its own invalidated producer.
	captured := maps.Clone(row)
	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return NICConfigPublication{}, err
	}

	var p NICConfigPublication
	if ids[nicConfigPublicationKey] == "" && target.Legacy {
		// An upstream-created consumer keeps its synthesized producer until the reload adopts it.
		p = target
	} else {
		p, err = decodeNICConfig(ids[nicConfigPublicationKey])
		if err != nil {
			return p, err
		}
	}

	if !maps.Equal(p.ACLIDs, target.ACLIDs) {
		if p.Phase != target.Phase || planned.ACLIDs == nil || !maps.Equal(p.ACLIDs, planned.ACLIDs) {
			return p, errors.New("Shared reload ACL identity differs from original and preselected plan")
		}

		target.ACLIDs = maps.Clone(planned.ACLIDs)
	}

	if p.Phase == "pending" {
		p.Phase = target.Phase
	}

	wire, err := publicationWire(p)
	if err != nil {
		return p, err
	}

	ids[nicConfigPublicationKey] = wire
	captured["external_ids"] = nicCleanupStringMapWire(ids)
	switch {
	case ids[ovnExtIDIncusLocation] != original.Location:
		return p, errors.New("Shared reload producer location changed")
	case p.Operation != target.Operation:
		return p, errors.New("Shared reload producer operation changed")
	case !nicProducerSettingsEqual(p.Settings, target.Settings):
		return p, errors.New("Shared reload producer settings changed")
	case p.Phase != target.Phase:
		return p, errors.New("Shared reload producer phase changed")
	}

	enabled, err := nicCleanupPortEnabled(row["enabled"])
	if err != nil || enabled != original.Enabled {
		return p, errors.New("Shared reload producer enabled state changed")
	}

	return s.nicConfig(captured, sw, target, false)
}

func (s *physicalReferences) pinnedSharedProducer(row, sw ovsdb.Row, target NICConfigPublication, originals map[OVNSwitchPort]NICReplayProducer, capture bool) error {
	producer, err := s.nicReplayProducer(row, sw, target)
	if err != nil {
		return err
	}

	name, validName := row["name"].(string)
	if !validName {
		return errors.New("Shared resource original producer has an invalid name")
	}

	port := OVNSwitchPort(name)
	if capture {
		originals[port] = producer
		return nil
	}

	original, found := originals[port]
	if !found || !reflect.DeepEqual(original, producer) {
		return errors.New("Shared resource original producer changed")
	}

	return nil
}

func (s *physicalReferences) pinnedGroupInfrastructure(row, group ovsdb.Row, projectID int64, targets map[OVNSwitchPort]NICConfigPublication, infrastructure map[string]ovsdb.Row, capture bool) (bool, error) {
	ids, err := nicCleanupStringMap(group["external_ids"])
	if err != nil {
		return false, err
	}

	groupName, validGroup := group["name"].(string)
	portName, validPort := row["name"].(string)
	if !validGroup || !validPort {
		return false, errors.New("Shared resource infrastructure has an invalid name")
	}

	for _, target := range targets {
		switchName := fmt.Sprintf("incus-net%d-ls-int", target.NetworkID)
		if target.ProjectID != projectID || ids[ovnExtIDIncusSwitch] != switchName || !strings.HasSuffix(groupName, fmt.Sprintf("_net%d", target.NetworkID)) {
			continue
		}

		referenceErr := s.networkPortsConsistent(target.NetworkID)
		if referenceErr != nil {
			return false, referenceErr
		}

		for _, sw := range s.rows["Logical_Switch"] {
			if sw["name"] != switchName {
				continue
			}

			ports, err := physicalUUIDs(sw["ports"])
			if err != nil {
				return false, err
			}

			pid, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return false, err
			}

			opts, err := nicCleanupStringMap(row["options"])
			if err != nil {
				return false, err
			}

			routerPort := opts["router-port"]
			if !ports[pid] || !s.networkInfrastructure(row, switchName, routerPort) || routerPort != fmt.Sprintf("incus-net%d-lr-lrp-int", target.NetworkID) {
				continue
			}
			// Infrastructure must retain its exact rooted row and switch parent throughout this caller.
			pinned := maps.Clone(row)
			pinned["parent_uuid"] = sw["_uuid"]
			if capture {
				infrastructure[portName] = pinned
			} else if !reflect.DeepEqual(infrastructure[portName], pinned) {
				return false, errors.New("Shared resource infrastructure changed")
			}

			return true, nil
		}
	}

	return false, nil
}

func (s *physicalReferences) applicableGroup(group ovsdb.Row, projectID int64, targets map[OVNSwitchPort]NICConfigPublication, originals map[OVNSwitchPort]NICReplayProducer, infrastructure map[string]ovsdb.Row, capture bool) error {
	ids, err := nicCleanupStringMap(group["external_ids"])
	if err != nil || ids[ovnExtIDIncusProjectID] != fmt.Sprint(projectID) {
		return errors.New("Shared resource group has unknown/foreign project ownership")
	}

	ports, err := physicalUUIDs(group["ports"])
	if err != nil {
		return err
	}

	for id := range ports {
		found := false
		for _, row := range s.rows["Logical_Switch_Port"] {
			rid, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return err
			}

			if rid != id {
				continue
			}

			name, validName := row["name"].(string)
			if !validName {
				return errors.New("Shared resource consumer has an invalid name")
			}

			target, ok := targets[OVNSwitchPort(name)]
			if !ok {
				found, err = s.pinnedGroupInfrastructure(row, group, projectID, targets, infrastructure, capture)
				if err != nil {
					return err
				}

				if found {
					continue
				}
			}

			if !ok || target.ProjectID != projectID {
				return errors.New("Physical shared resource consumer is absent from current candidates")
			}

			for _, sw := range s.rows["Logical_Switch"] {
				if sw["name"] != fmt.Sprintf("incus-net%d-ls-int", target.NetworkID) {
					continue
				}

				err = s.pinnedSharedProducer(row, sw, target, originals, capture)
				if err != nil {
					return err
				}

				found = true
			}
		}

		if !found {
			return errors.New("Physical shared resource consumer has no evidenced original parent")
		}
	}

	return nil
}

func (s *physicalReferences) aclApplicable(projectID, aclID int64, targets map[OVNSwitchPort]NICConfigPublication, originals map[OVNSwitchPort]NICReplayProducer, infrastructure map[string]ovsdb.Row, capture bool) error {
	for _, row := range s.rows["Logical_Switch_Port"] {
		ids, err := nicCleanupStringMap(row["external_ids"])
		if err != nil {
			return err
		}

		if ids[nicConfigPublicationKey] == "" {
			continue
		}

		p, err := decodeNICConfig(ids[nicConfigPublicationKey])
		if err != nil {
			return errors.New("Shared ACL replay encountered malformed producer evidence")
		}

		retains := false
		for _, id := range p.ACLIDs {
			if id == aclID {
				retains = true
			}
		}

		if !retains {
			continue
		}

		name, validName := row["name"].(string)
		if !validName {
			return errors.New("Shared resource consumer has an invalid name")
		}

		target, ok := targets[OVNSwitchPort(name)]
		if !ok {
			return errors.New("Shared ACL original producer is absent from selected candidates")
		}

		matched := false
		for _, sw := range s.rows["Logical_Switch"] {
			id, e := nicCleanupRowUUID(sw, "_uuid")
			if e != nil {
				return e
			}

			if id != p.SwitchUUID {
				continue
			}

			e = s.pinnedSharedProducer(row, sw, target, originals, capture)
			if e != nil {
				return e
			}

			matched = true
		}

		if !matched {
			return errors.New("Shared ACL original producer parent is absent")
		}
	}

	prefix := fmt.Sprintf("incus_acl%d", aclID)
	for _, group := range s.rows["Port_Group"] {
		name, validName := group["name"].(string)
		if !validName {
			return errors.New("Shared resource backend row has an invalid name")
		}

		if name != prefix && !strings.HasPrefix(name, prefix+"_") {
			continue
		}

		err := s.applicableGroup(group, projectID, targets, originals, infrastructure, capture)
		if err != nil {
			return err
		}

		ports, err := physicalUUIDs(group["ports"])
		if err != nil {
			return err
		}

		err = s.groupUnusedExcept(name, projectID, ports)
		if err != nil {
			return err
		}
	}

	return nil
}

// GuardACLReferenceUpdate covers the old physical resource even when Desired omits it.
func (o *NB) GuardACLReferenceUpdate(ctx context.Context, projectID, aclID int64, targets map[OVNSwitchPort]NICConfigPublication) (*NB, error) {
	if projectID <= 0 || aclID <= 0 {
		return nil, errors.New("Invalid shared ACL identity")
	}

	originals := map[OVNSwitchPort]NICReplayProducer{}
	infrastructure := map[string]ovsdb.Row{}
	capture := true
	check := func(s *physicalReferences) error {
		return s.aclApplicable(projectID, aclID, targets, originals, infrastructure, capture)
	}

	snapshot, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	err = check(snapshot)
	if err != nil {
		return nil, err
	}

	capture = false
	return o.withReferenceMutation(check), nil
}

func (s *physicalReferences) setApplicable(projectID, setID int64, targets map[OVNSwitchPort]NICConfigPublication, originals map[OVNSwitchPort]NICReplayProducer, infrastructure map[string]ovsdb.Row, capture bool) error {
	prefix := fmt.Sprintf("incus_set%d", setID)
	for _, table := range []string{"ACL", "Logical_Router_Policy"} {
		for _, row := range s.rows[table] {
			match, validMatch := row["match"].(string)
			if !validMatch {
				return errors.New("Shared resource backend row has an invalid match")
			}

			used := false
			for _, ref := range physicalMatchReference.FindAllString(match, -1) {
				if ref == "$"+prefix+"_ip4" || ref == "$"+prefix+"_ip6" {
					used = true
				}
			}

			if !used {
				continue
			}

			if table != "ACL" {
				return errors.New("Address set has an unknown physical router-policy subject")
			}

			id, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return err
			}

			parents := 0
			for _, group := range s.rows["Port_Group"] {
				acls, err := physicalUUIDs(group["acls"])
				if err != nil {
					return err
				}

				if !acls[id] {
					continue
				}

				name, validName := group["name"].(string)

				if !validName {
					return errors.New("Shared resource backend row has an invalid name")
				}

				if !strings.HasPrefix(name, "incus_acl") {
					return errors.New("Address set has a foreign physical rule subject")
				}

				err = s.applicableGroup(group, projectID, targets, originals, infrastructure, capture)
				if err != nil {
					return err
				}

				parents++
			}

			for _, sw := range s.rows["Logical_Switch"] {
				acls, err := physicalUUIDs(sw["acls"])
				if err != nil {
					return err
				}

				if acls[id] {
					return errors.New("Address set has an unsupported logical-switch rule subject")
				}
			}

			if parents == 0 {
				return errors.New("Address set physical rule ownership is unknown")
			}
		}
	}

	return nil
}

// GuardAddressSetReferenceUpdate admits only physically evidenced current consumers and owned rule subjects.
func (o *NB) GuardAddressSetReferenceUpdate(ctx context.Context, projectID, setID int64, targets map[OVNSwitchPort]NICConfigPublication) (*NB, error) {
	if projectID <= 0 || setID <= 0 {
		return nil, errors.New("Invalid shared address-set identity")
	}

	originals := map[OVNSwitchPort]NICReplayProducer{}
	infrastructure := map[string]ovsdb.Row{}
	capture := true
	check := func(s *physicalReferences) error {
		return s.setApplicable(projectID, setID, targets, originals, infrastructure, capture)
	}

	snapshot, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	err = check(snapshot)
	if err != nil {
		return nil, err
	}

	capture = false
	return o.withReferenceMutation(check), nil
}

// ReferenceMutationGuarded identifies a bounded caller copy for preserving its guard across reload.
func (o *NB) ReferenceMutationGuarded() bool {
	_, ok := o.client.(*referenceMutationClient)
	return ok
}

// GuardNetworkDelete rechecks physical references at every router/switch effect transaction.
func (o *NB) GuardNetworkDelete(networkID int64, routerPort string) *NB {
	return o.withReferenceMutation(func(s *physicalReferences) error { return s.networkUnused(networkID, routerPort) })
}

// GuardExistingPortGroup admits reuse and pins its managed identity through later effects.
func (o *NB) GuardExistingPortGroup(ctx context.Context, projectID int64, name OVNPortGroup, associated []OVNPortGroup, networkID int64, initial ...OVNSwitchPort) (*NB, bool, error) {
	if networkID <= 0 {
		return nil, false, errors.New("Invalid numeric network identity")
	}

	sw := OVNSwitch(fmt.Sprintf("incus-net%d-ls-int", networkID))
	routerPort := fmt.Sprintf("incus-net%d-lr-lrp-int", networkID)
	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, false, err
	}

	var original ovsdb.Row
	infrastructure := map[string]ovsdb.Row{}
	for _, group := range s.rows["Port_Group"] {
		if group["name"] != string(name) {
			continue
		}

		if original != nil {
			return nil, false, errors.New("Existing managed port group is ambiguous")
		}

		ids, err := nicCleanupStringMap(group["external_ids"])
		if err != nil {
			return nil, false, err
		}

		parts := make([]string, 0, len(associated))
		for _, value := range associated {
			parts = append(parts, string(value))
		}

		if projectID <= 0 || ids[ovnExtIDIncusProjectID] != fmt.Sprint(projectID) || ids[ovnExtIDIncusSwitch] != string(sw) || ids[ovnExtIDIncusPortGroup] != strings.Join(parts, ",") {
			return nil, false, errors.New("Existing managed port group identity differs from selected network")
		}

		ports, err := physicalUUIDs(group["ports"])
		if err != nil {
			return nil, false, err
		}

		if len(ports) != len(initial) {
			return nil, false, errors.New("Existing managed port group has an unknown additional member")
		}

		referenceErr := s.networkPortsConsistent(networkID)
		if referenceErr != nil {
			return nil, false, referenceErr
		}

		for _, port := range initial {
			matched := false
			for _, row := range s.rows["Logical_Switch_Port"] {
				if row["name"] != string(port) {
					continue
				}

				id, err := nicCleanupRowUUID(row, "_uuid")
				if err != nil {
					return nil, false, err
				}

				for _, parent := range s.rows["Logical_Switch"] {
					if parent["name"] != string(sw) {
						continue
					}

					parentPorts, err := physicalUUIDs(parent["ports"])
					if err != nil {
						return nil, false, err
					}

					if !ports[id] || !parentPorts[id] || !s.networkInfrastructure(row, string(sw), routerPort) {
						continue
					}

					identity := maps.Clone(row)
					delete(identity, "_version")
					identity["parent_uuid"] = parent["_uuid"]
					infrastructure[string(port)] = identity
					matched = true
				}
			}

			if !matched {
				return nil, false, errors.New("Existing managed port group original infrastructure member changed")
			}
		}

		original = maps.Clone(group)
		delete(original, "_version")
		delete(original, "acls")
	}

	_, err = o.nicCleanupTransact(ctx, s.waits()...)
	if err != nil {
		return nil, false, err
	}

	if original == nil {
		return o, false, nil
	}

	guarded := o.withReferenceMutation(func(current *physicalReferences) error {
		referenceErr := current.networkPortsConsistent(networkID)
		if referenceErr != nil {
			return referenceErr
		}

		found := false
		for _, row := range current.rows["Port_Group"] {
			if row["name"] != string(name) {
				continue
			}

			identity := maps.Clone(row)
			delete(identity, "_version")
			delete(identity, "acls")
			if found || !reflect.DeepEqual(original, identity) {
				return errors.New("Reused managed port group original identity changed")
			}

			found = true
		}

		if !found {
			return errors.New("Reused managed port group original disappeared")
		}

		for port, originalPort := range infrastructure {
			matched := false
			for _, row := range current.rows["Logical_Switch_Port"] {
				if row["name"] != port {
					continue
				}

				identity := maps.Clone(row)
				delete(identity, "_version")
				for _, parent := range current.rows["Logical_Switch"] {
					if parent["name"] != string(sw) {
						continue
					}

					ports, err := physicalUUIDs(parent["ports"])
					if err != nil {
						return err
					}

					id, err := nicCleanupRowUUID(row, "_uuid")
					if err != nil {
						return err
					}

					identity["parent_uuid"] = parent["_uuid"]
					if ports[id] && reflect.DeepEqual(originalPort, identity) {
						matched = true
					}
				}
			}

			if !matched {
				return errors.New("Reused managed port group infrastructure changed")
			}
		}

		return nil
	})
	return guarded, true, nil
}
