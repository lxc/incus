package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// nicParentPrefixReload pins live allocations moving between two existing parent proxies.
type nicParentPrefixReload struct {
	root   string
	old    NICPortCleanup
	new    NICPortCleanup
	owners map[string]nicPrefixContribution
}

// PrepareNICParentPrefixReload captures an ordinary parent change's exact final publication callback.
func (o *NB) PrepareNICParentPrefixReload(ctx context.Context, oldSwitch OVNSwitch, oldPort OVNSwitchPort, newSwitch OVNSwitch, newPort OVNSwitchPort) (func(context.Context, OVNSwitch, map[OVNSwitchPort]NICConfigPublication) error, error) {
	plan, err := o.captureNICParentPrefixReload(ctx, oldSwitch, oldPort, newSwitch, newPort)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, sw OVNSwitch, targets map[OVNSwitchPort]NICConfigPublication) error {
		return o.completeNICParentReload(ctx, sw, targets, plan)
	}, nil
}

// captureNICParentPrefixReload selects original contributions before ordinary parent Update effects.
func (o *NB) captureNICParentPrefixReload(ctx context.Context, oldSwitch OVNSwitch, oldPort OVNSwitchPort, newSwitch OVNSwitch, newPort OVNSwitchPort) (*nicParentPrefixReload, error) {
	guarded, ok := o.client.(*referenceMutationClient)
	if !ok {
		return nil, errors.New("Parent prefix replay lacks enclosing producer guard")
	}

	old, row, err := o.publicationPort(ctx, oldSwitch, oldPort)
	if err != nil {
		return nil, err
	}

	next, nextRow, err := o.publicationPort(ctx, newSwitch, newPort)
	if err != nil {
		return nil, err
	}

	if row == nil || nextRow == nil || row["type"] != "router" || nextRow["type"] != "router" || old.PortUUID == next.PortUUID {
		return nil, errors.New("Parent prefix replay requires two original router proxies")
	}

	plan := &nicParentPrefixReload{root: o.backendID, old: old, new: next, owners: map[string]nicPrefixContribution{}}
	guards := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(old), nicCleanupPortOwnerWait(old), nicCleanupPortParentWait(next), nicCleanupPortOwnerWait(next), nicPrefixRowWait("Logical_Switch_Port", row), nicPrefixRowWait("Logical_Switch_Port", nextRow)}
	for port, producer := range guarded.replay {
		p := producer.Publication
		if p.Phase != "start" || p.Legacy {
			continue
		}

		identity, sourceRow, err := o.publicationPort(ctx, OVNSwitch(fmt.Sprintf("incus-net%d-ls-int", p.NetworkID)), port)
		if err != nil {
			return nil, err
		}

		if sourceRow == nil {
			return nil, errors.New("Parent prefix replay original NIC disappeared")
		}

		ids, err := nicCleanupStringMap(sourceRow["external_ids"])
		if err != nil {
			return nil, err
		}

		var owner NICPrefixOwner
		if json.Unmarshal([]byte(ids[nicPrefixGeneration]), &owner) != nil || owner.Validate(o.backendID, identity) != nil || owner.Generation != p.Generation || owner.Source != p.Source || ids[ovnExtIDIncusLocation] != p.Source || owner.PortUUID != p.PortUUID || owner.SwitchUUID != p.SwitchUUID {
			return nil, errors.New("Parent prefix replay differs from original NIC allocation")
		}

		ledger, _, err := o.nicPrefixLedger("Logical_Switch_Port", row, false)
		if err != nil {
			return nil, err
		}

		contribution, found := ledger.Owners[owner.Generation]
		if !found || nicPrefixEncode(contribution.Owner) != nicPrefixEncode(owner) {
			return nil, errors.New("Parent prefix replay lacks original contribution")
		}

		nextLedger, _, err := o.nicPrefixLedger("Logical_Switch_Port", nextRow, true)
		if err != nil {
			return nil, err
		}

		_, active := nextLedger.Owners[owner.Generation]
		_, retired := nextLedger.Released[owner.Generation]
		if active || retired {
			return nil, errors.New("Parent prefix replay target already claims original generation")
		}

		plan.owners[owner.Generation] = contribution
		guards = append(guards, nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity), nicPublicationRowWait(sourceRow))
	}

	_, err = o.nicCleanupTransact(ctx, guards...)
	if err != nil {
		return nil, err
	}

	return plan, nil
}

func (o *NB) nicParentPrefixOperations(ctx context.Context, plan *nicParentPrefixReload) ([]ovsdb.Operation, []ovsdb.Operation, error) {
	if plan == nil || plan.root != o.backendID {
		return nil, nil, errors.New("Parent prefix replay backend changed")
	}

	old, oldRow, err := o.publicationPort(ctx, plan.old.SwitchName, plan.old.PortName)
	if err != nil {
		return nil, nil, err
	}

	next, nextRow, err := o.publicationPort(ctx, plan.new.SwitchName, plan.new.PortName)
	if err != nil {
		return nil, nil, err
	}

	if oldRow == nil || nextRow == nil || old != plan.old || next != plan.new || oldRow["type"] != "router" || nextRow["type"] != "router" {
		return nil, nil, errors.New("Parent prefix replay proxy identity changed")
	}

	verify := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(old), nicCleanupPortOwnerWait(old), nicCleanupPortParentWait(next), nicCleanupPortOwnerWait(next)}
	ops := append([]ovsdb.Operation{}, verify...)
	ops = append(ops, nicPrefixRowWait("Logical_Switch_Port", oldRow), nicPrefixRowWait("Logical_Switch_Port", nextRow))
	if len(plan.owners) == 0 {
		return ops, ops, nil
	}

	oldLedger, oldIDs, err := o.nicPrefixLedger("Logical_Switch_Port", oldRow, false)
	if err != nil {
		return nil, nil, err
	}

	nextLedger, nextIDs, err := o.nicPrefixLedger("Logical_Switch_Port", nextRow, true)
	if err != nil {
		return nil, nil, err
	}

	for generation, contribution := range plan.owners {
		current, found := oldLedger.Owners[generation]
		if !found || nicPrefixEncode(current) != nicPrefixEncode(contribution) {
			return nil, nil, errors.New("Parent prefix replay original contribution changed")
		}

		_, active := nextLedger.Owners[generation]
		_, retired := nextLedger.Released[generation]
		if active || retired {
			return nil, nil, errors.New("Parent prefix replay target already claims original generation")
		}

		delete(oldLedger.Owners, generation)
		nextLedger.Owners[generation] = contribution
	}

	oldUpdate := nicPrefixUpdate("Logical_Switch_Port", oldRow, oldLedger, oldIDs)
	nextUpdate := nicPrefixUpdate("Logical_Switch_Port", nextRow, nextLedger, nextIDs)
	oldExpected, nextExpected := maps.Clone(oldRow), maps.Clone(nextRow)
	maps.Copy(oldExpected, oldUpdate.Row)
	maps.Copy(nextExpected, nextUpdate.Row)
	verify = append(verify, nicPrefixRowWait("Logical_Switch_Port", oldExpected), nicPrefixRowWait("Logical_Switch_Port", nextExpected))
	return append(ops, oldUpdate, nextUpdate), verify, nil
}

// completeNICParentReload atomically moves exact shared provenance and completes every selected producer.
func (o *NB) completeNICParentReload(ctx context.Context, sw OVNSwitch, targets map[OVNSwitchPort]NICConfigPublication, plan *nicParentPrefixReload) error {
	guarded, ok := o.client.(*referenceMutationClient)
	if !ok || len(targets) != len(guarded.replay) {
		return errors.New("Parent reload completion lacks all selected original producers")
	}

	ops, verify, err := o.nicParentPrefixOperations(ctx, plan)
	if err != nil {
		return err
	}

	for port, target := range targets {
		identity, row, p, err := o.nicConfigReloadPublication(ctx, sw, port, target)
		if err != nil {
			return err
		}

		p.Settings = publicationSettings(row)
		err = p.valid(o.backendID)
		if err != nil {
			return err
		}

		encoded, err := publicationWire(p)
		if err != nil {
			return err
		}

		ids, err := nicCleanupStringMap(row["external_ids"])
		if err != nil {
			return err
		}

		if p.Phase == "start" && ids[ovnExtIDIncusLocation] != p.Source {
			return errors.New("NIC producer source location changed")
		}

		for _, contribution := range plan.owners {
			owner := contribution.Owner
			if owner.PortName == port && (ids[nicPrefixGeneration] != nicPrefixEncode(owner) || p.Generation != owner.Generation || p.Source != owner.Source || p.PortUUID != owner.PortUUID || p.SwitchUUID != owner.SwitchUUID) {
				return errors.New("Parent prefix replay NIC allocation marker changed")
			}
		}

		ids = maps.Clone(ids)
		ids[nicConfigPublicationKey] = encoded
		ops = append(ops, nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity), nicPublicationRowWait(row), ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
		expected := maps.Clone(row)
		expected["external_ids"] = nicCleanupStringMapWire(ids)
		verify = append(verify, nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity), nicPublicationRowWait(expected))
	}

	_, err = o.nicCleanupTransact(ctx, ops...)
	if err == nil {
		return nil
	}

	// An uncertain reply is acknowledged only by every exact final row under one rooted read guard.

	_, verifyErr := o.nicCleanupTransact(ctx, verify...)
	if verifyErr != nil {
		return errors.Join(err, verifyErr)
	}

	return nil
}
