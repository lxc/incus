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
	"strings"
	"time"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

const nicConfigPublicationKey = "incus:nic-config-publication"

// NICConfigPublication is producer evidence for a single rooted physical NIC port.
type NICConfigPublication struct {
	Version      int
	RootUUID     string
	SwitchUUID   string
	PortUUID     string
	Generation   string
	Operation    string
	NetworkID    int64
	ProjectID    int64
	InstanceUUID string
	Device       string
	Source       string
	Phase        string
	Input        map[string]string
	ACLIDs       map[string]int64
	MAC          string
	IPs          []string
	Settings     map[string]any
	// Legacy marks a producer adopted from an upstream-created port that has no allocation
	// generation yet; the next Start publishes a fresh, non-legacy producer.
	Legacy bool `json:",omitempty"`
}

func (p NICConfigPublication) valid(root string) error {
	if p.Version != 1 || p.RootUUID != root || !nicCleanupUUID(root) || !nicCleanupUUID(p.SwitchUUID) || !nicCleanupUUID(p.PortUUID) || !nicCleanupUUID(p.Generation) || !nicCleanupUUID(p.InstanceUUID) || p.NetworkID <= 0 || p.ProjectID <= 0 || p.Device == "" || p.Source == "" || (p.Phase != "add" && p.Phase != "start" && p.Phase != "pending") || p.Input == nil || p.ACLIDs == nil {
		return errors.New("NIC config publication identity is missing or invalid")
	}

	if p.Operation != "" && !nicCleanupUUID(p.Operation) {
		return errors.New("NIC config publication operation is invalid")
	}

	return nil
}

// nicLegacyPort reports an upstream-created NIC port: it carries neither producer nor allocation
// evidence and its name and parent are exactly those of the selected instance device.
func nicLegacyPort(row ovsdb.Row, ids map[string]string, sw string, target NICConfigPublication) bool {
	return ids[nicConfigPublicationKey] == "" && nicPrefixLegacyMarker(ids) && ids[ovnExtIDIncusSwitch] == sw && target.NetworkID > 0 && nicCleanupUUID(target.InstanceUUID) && target.Device != "" && row["name"] == fmt.Sprintf("incus-net%d-instance-%s-%s", target.NetworkID, target.InstanceUUID, target.Device)
}

func publicationSettings(row ovsdb.Row) map[string]any {
	return map[string]any{"addresses": row["addresses"], "dynamic_addresses": row["dynamic_addresses"], "port_security": row["port_security"]}
}

// nicProducerSettingsEqual compares producer-written settings; dynamic addresses are northd-owned and
// may legitimately be reassigned or recorded only by some publications.
func nicProducerSettingsEqual(a, b map[string]any) bool {
	written := func(settings map[string]any) map[string]any {
		result := maps.Clone(settings)
		delete(result, "dynamic_addresses")
		return result
	}

	return reflect.DeepEqual(written(a), written(b))
}

// publicationSettingsWire returns the producer-written settings of an adopted upstream port as a
// decoded publication records them; northd-owned dynamic addresses are not part of them.
func publicationSettingsWire(row ovsdb.Row) (map[string]any, error) {
	written := publicationSettings(row)
	delete(written, "dynamic_addresses")
	data, err := json.Marshal(written)
	if err != nil {
		return nil, err
	}

	var settings map[string]any
	err = json.Unmarshal(data, &settings)
	return settings, err
}

func publicationWire(p NICConfigPublication) (string, error) {
	b, err := json.Marshal(p)
	return string(b), err
}

func (o *NB) publicationPort(ctx context.Context, sw OVNSwitch, port OVNSwitchPort) (NICPortCleanup, ovsdb.Row, error) {
	results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(sw)}}}, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(port)}}})
	if err != nil {
		return NICPortCleanup{}, nil, err
	}

	if len(results[1].Rows) != 1 || len(results[2].Rows) > 1 {
		return NICPortCleanup{}, nil, errors.New("NIC config publication switch/port is absent or ambiguous")
	}

	sid, err := nicCleanupRowUUID(results[1].Rows[0], "_uuid")
	if err != nil {
		return NICPortCleanup{}, nil, err
	}

	if len(results[2].Rows) == 0 {
		return NICPortCleanup{RootUUID: o.backendID, SwitchUUID: sid, SwitchName: sw, PortName: port}, nil, nil
	}

	row := results[2].Rows[0]
	pid, err := nicCleanupRowUUID(row, "_uuid")
	if err != nil {
		return NICPortCleanup{}, nil, err
	}

	ports, err := physicalUUIDs(results[1].Rows[0]["ports"])
	if err != nil || !ports[pid] {
		return NICPortCleanup{}, nil, errors.New("NIC config publication port parent changed")
	}

	return NICPortCleanup{RootUUID: o.backendID, SwitchUUID: sid, SwitchName: sw, PortUUID: pid, PortName: port}, row, nil
}

func (o *NB) setPublication(ctx context.Context, plan NICPortCleanup, row ovsdb.Row, p NICConfigPublication) error {
	encoded, err := publicationWire(p)
	if err != nil {
		return err
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return err
	}

	ids[nicConfigPublicationKey] = encoded
	ops := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicPublicationRowWait(row), {Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}}}
	_, err = o.nicCleanupTransact(ctx, ops...)
	if err != nil {
		// A lost reply can only be acknowledged by the same exact rooted publication.
		_, current, readErr := o.publicationPort(ctx, plan.SwitchName, plan.PortName)
		if readErr != nil || current == nil || current["_uuid"] != row["_uuid"] {
			return errors.Join(err, readErr)
		}

		currentIDs, readErr := nicCleanupStringMap(current["external_ids"])
		if readErr != nil || currentIDs[nicConfigPublicationKey] != encoded {
			return errors.Join(err, readErr)
		}

		_, readErr = o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicPublicationRowWait(current))
		return readErr
	}

	return nil
}

// BeginNICConfigPublication invalidates an old receipt before the first backend producer effect.
func (o *NB) BeginNICConfigPublication(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, selected ...NICConfigPublication) error {
	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return err
	}

	if row == nil {
		return nil
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return err
	}

	if ids[nicConfigPublicationKey] == "" {
		// An upstream-created port is adopted only by its exact selected instance device.
		if len(selected) == 0 || !nicLegacyPort(row, ids, string(sw), selected[0]) {
			return errors.New("Existing NIC lacks original producer evidence; legacy adoption is unsupported")
		}

		enabled, err := nicCleanupPortEnabled(row["enabled"])
		if err != nil || enabled && ids[ovnExtIDIncusLocation] != selected[0].Source {
			return errors.Join(err, errors.New("Upstream NIC port is active on another member"))
		}

		p := selected[0]
		p.Version, p.RootUUID, p.SwitchUUID, p.PortUUID = 1, o.backendID, plan.SwitchUUID, plan.PortUUID
		p.Generation, p.Phase, p.Legacy = plan.PortUUID, "pending", true
		p.Settings, err = publicationSettingsWire(row)
		if err != nil {
			return err
		}

		err = p.valid(o.backendID)
		if err != nil {
			return err
		}

		return o.setPublication(ctx, plan, row, p)
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return err
	}

	err = p.valid(o.backendID)
	if err != nil {
		return err
	}

	if p.PortUUID != plan.PortUUID || p.SwitchUUID != plan.SwitchUUID {
		return errors.New("NIC config publication port was replaced")
	}

	if len(selected) > 0 {
		target := selected[0]
		if p.NetworkID != target.NetworkID || p.ProjectID != target.ProjectID || p.InstanceUUID != target.InstanceUUID || p.Device != target.Device {
			return errors.New("NIC original producer identity differs from selected caller")
		}

		if p.Source != target.Source {
			return &NICProducerSourceError{Source: p.Source}
		}

		if p.Phase == "pending" && (!nicInputsEqual(p.Input, target.Input) || !maps.Equal(p.ACLIDs, target.ACLIDs)) {
			return errors.New("NIC pending original configuration cannot be replaced by a different retry")
		}
	}

	p.Phase = "pending"
	return o.setPublication(ctx, plan, row, p)
}

// NICConfigPublicationSource returns the member that published the port's original producer,
// or "" when the port or its publication is absent.
func (o *NB) NICConfigPublicationSource(ctx context.Context, sw OVNSwitch, port OVNSwitchPort) (string, error) {
	_, row, err := o.publicationPort(ctx, sw, port)
	if err != nil || row == nil {
		return "", err
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil || ids[nicConfigPublicationKey] == "" {
		return "", err
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return "", err
	}

	err = p.valid(o.backendID)
	if err != nil {
		return "", err
	}

	return p.Source, nil
}

// NICConfigPublicationOf returns the port's valid original producer publication.
func (o *NB) NICConfigPublicationOf(ctx context.Context, sw OVNSwitch, port OVNSwitchPort) (NICConfigPublication, error) {
	_, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return NICConfigPublication{}, err
	}

	if row == nil {
		return NICConfigPublication{}, errors.New("NIC config publication port is absent")
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return NICConfigPublication{}, err
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return NICConfigPublication{}, err
	}

	return p, p.valid(o.backendID)
}

// NICProducerSourceError reports an otherwise identical original producer published by another member.
type NICProducerSourceError struct {
	Source string
}

func (e *NICProducerSourceError) Error() string {
	return "NIC original producer identity differs from selected caller"
}

// BeginNICConfigPublicationTransfer hands a completed original producer publication from another
// member to the selected caller. The caller must first establish that the instance is placed on it
// and that the original member acknowledged its NIC cleanup. Only a disabled, unbound port with a
// completed Add or Start outcome from exactly that member can be transferred; identity must match.
func (o *NB) BeginNICConfigPublicationTransfer(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, target NICConfigPublication, from string) error {
	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return err
	}

	if row == nil {
		return errors.New("Cannot transfer an absent NIC config publication")
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return err
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return err
	}

	err = p.valid(o.backendID)
	if err != nil {
		return err
	}

	if p.PortUUID != plan.PortUUID || p.SwitchUUID != plan.SwitchUUID {
		return errors.New("NIC config publication port was replaced")
	}

	if from == "" || from == target.Source || p.Source != from || (p.Phase != "add" && p.Phase != "start") || p.NetworkID != target.NetworkID || p.ProjectID != target.ProjectID || p.InstanceUUID != target.InstanceUUID || p.Device != target.Device {
		return errors.New("NIC original producer cannot be transferred to the selected caller")
	}

	enabled, enabledSet := publicationOptionalBool(row["enabled"])
	up, _ := publicationOptionalBool(row["up"])
	if !enabledSet || enabled || up {
		return errors.New("NIC original producer port is still active")
	}

	p.Source = target.Source
	p.Input = target.Input
	p.ACLIDs = target.ACLIDs
	p.Phase = "pending"
	return o.setPublication(ctx, plan, row, p)
}

// publicationOptionalBool decodes an optional OVSDB boolean, which may be a bare value or a set.
func publicationOptionalBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case ovsdb.OvsSet:
		if len(v.GoSet) == 1 {
			b, ok := v.GoSet[0].(bool)
			return b, ok
		}
	}

	return false, false
}

// PublishNICConfig records an actual completed Add or Start outcome after its relevant effects.
func (o *NB) PublishNICConfig(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, p NICConfigPublication) error {
	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return err
	}

	if row == nil {
		return errors.New("Cannot publish absent NIC config outcome")
	}

	p.Version = 1
	p.RootUUID = o.backendID
	p.SwitchUUID = plan.SwitchUUID
	p.PortUUID = plan.PortUUID
	p.Settings = publicationSettings(row)
	err = p.valid(o.backendID)
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

	return o.setPublication(ctx, plan, row, p)
}

// nicAddAddresses derives the exact request already written by CreateLogicalSwitchPort.
func nicAddAddresses(opts *OVNSwitchPortOpts) ([]string, bool, error) {
	if opts == nil || opts.MAC == nil || opts.Enabled == nil || *opts.Enabled || opts.RouterPort != "" {
		return nil, false, errors.New("Disabled normal Add port options are required")
	}

	if (opts.IPV4 == "none" && opts.IPV6 != "none") || (opts.IPV6 == "none" && opts.IPV4 != "none") {
		return nil, false, errors.New("OVN doesn't support disabling IP allocation on only one protocol")
	}

	addresses := []string{}
	dynamic := false
	if opts.IPV4 != "none" && opts.IPV6 != "none" {
		parts := []string{opts.MAC.String()}
		if opts.IPV4 == "" && opts.IPV6 == "" {
			parts = append(parts, "dynamic")
			dynamic = true
		} else {
			if opts.IPV4 != "" {
				parts = append(parts, opts.IPV4)
			}

			if opts.IPV6 != "" {
				parts = append(parts, opts.IPV6)
			}
		}

		addresses = append(addresses, strings.Join(parts, " "))
	}

	if opts.Promiscuous {
		addresses = append(addresses, "unknown")
	}

	slices.Sort(addresses)
	return addresses, dynamic, nil
}

// nicAddAllocationReady uses the switch IPAM configuration, independently of DHCP options.
func nicAddAllocationReady(value any, dynamic bool, mac string, ipam map[string]string) (bool, error) {
	values, err := nicCleanupStringSet(value)
	if err != nil || len(values) > 1 {
		return false, errors.New("Invalid normal Add dynamic address state")
	}

	prefixes := []*net.IPNet{}
	for _, key := range []string{"subnet", "ipv6_prefix"} {
		if ipam[key] == "" {
			continue
		}

		ip, prefix, err := net.ParseCIDR(ipam[key])
		if err != nil || key == "subnet" && ip.To4() == nil || key == "ipv6_prefix" && ip.To4() != nil {
			return false, errors.New("Invalid normal Add switch IPAM prefix")
		}

		prefixes = append(prefixes, prefix)
	}

	macOnly := ipam["mac_only"] == "true"
	if ipam["mac_only"] != "" && ipam["mac_only"] != "true" && ipam["mac_only"] != "false" {
		return false, errors.New("Invalid normal Add switch MAC-only allocation setting")
	}

	if !dynamic || len(prefixes) == 0 && !macOnly {
		// A previous dynamic allocation must clear after a static/disabled/no-IPAM request.
		return len(values) == 0, nil
	}

	if len(values) == 0 {
		return false, nil
	}

	parts := strings.Fields(values[0])
	if len(parts) == 0 || parts[0] != mac {
		return false, errors.New("Normal Add allocated MAC differs from the requested MAC")
	}

	families := map[int]bool{}
	for _, part := range parts[1:] {
		ip := net.ParseIP(part)
		if ip == nil {
			return false, errors.New("Invalid normal Add allocated IP")
		}

		family := 6
		if ip.To4() != nil {
			family = 4
		}

		if families[family] {
			return false, errors.New("Duplicate normal Add allocated address family")
		}

		found := false
		for _, prefix := range prefixes {
			if prefix.Contains(ip) {
				found = true
				break
			}
		}

		if !found {
			return false, errors.New("Normal Add allocated IP differs from the switch IPAM prefixes")
		}

		families[family] = true
	}

	return len(families) == len(prefixes), nil
}

func nicAddImmutable(row ovsdb.Row) ovsdb.Row {
	pinned := ovsdb.Row{}
	for _, key := range []string{"_uuid", "name", "type", "addresses", "port_security", "enabled", "external_ids", "parent_name", "tag_request", "dhcpv4_options", "dhcpv6_options", "options"} {
		pinned[key] = row[key]
	}

	return pinned
}

func nicAddIPAMRow(row ovsdb.Row) ovsdb.Row {
	return ovsdb.Row{"_uuid": row["_uuid"], "name": row["name"], "other_config": row["other_config"]}
}

func (o *NB) nicAddSnapshot(ctx context.Context, sw OVNSwitch, port OVNSwitchPort) (NICPortCleanup, ovsdb.Row, ovsdb.Row, error) {
	results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(sw)}}}, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(port)}}})
	if err != nil {
		return NICPortCleanup{}, nil, nil, err
	}

	if len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		return NICPortCleanup{}, nil, nil, errors.New("Normal Add switch/port is absent or ambiguous")
	}

	switchRow, row := results[1].Rows[0], results[2].Rows[0]
	sid, err := nicCleanupRowUUID(switchRow, "_uuid")
	if err != nil {
		return NICPortCleanup{}, nil, nil, err
	}

	pid, err := nicCleanupRowUUID(row, "_uuid")
	if err != nil {
		return NICPortCleanup{}, nil, nil, err
	}

	ports, err := physicalUUIDs(switchRow["ports"])
	if err != nil || !ports[pid] {
		return NICPortCleanup{}, nil, nil, errors.New("Normal Add port parent changed")
	}

	return NICPortCleanup{RootUUID: o.backendID, SwitchUUID: sid, SwitchName: sw, PortUUID: pid, PortName: port}, row, switchRow, nil
}

// PublishNICAddConfig completes only the normal disabled Add producer after OVN allocation converges.
func (o *NB) PublishNICAddConfig(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, p NICConfigPublication, opts *OVNSwitchPortOpts) error {
	addresses, dynamic, err := nicAddAddresses(opts)
	if err != nil {
		return err
	}

	mac, err := net.ParseMAC(p.Input["hwaddr"])
	if err != nil || mac.String() != opts.MAC.String() || p.Phase != "add" {
		return errors.New("Normal Add publication differs from the selected request")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var original, originalIPAM ovsdb.Row
	var attemptedSettings []byte
	for attempt := range 200 {
		err = ctx.Err()
		if err != nil {
			return fmt.Errorf("Normal Add publication did not converge: %w", err)
		}

		plan, row, switchRow, err := o.nicAddSnapshot(ctx, sw, port)
		if err != nil {
			return err
		}

		actual, err := nicCleanupStringSet(row["addresses"])
		if err != nil || !slices.Equal(addresses, actual) {
			return errors.New("Normal Add requested addresses changed")
		}

		enabled, err := nicCleanupPortEnabled(row["enabled"])
		if err != nil || enabled {
			return errors.New("Normal Add port is no longer disabled")
		}

		if row["type"] != "" {
			return errors.New("Normal Add port type changed")
		}

		ids, err := nicCleanupStringMap(row["external_ids"])
		if err != nil || ids[ovnExtIDIncusSwitch] != string(sw) {
			return errors.New("Normal Add port owner/parent changed")
		}

		if ids[nicConfigPublicationKey] != "" {
			var prior NICConfigPublication
			err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &prior)
			if err != nil || prior.valid(o.backendID) != nil || prior.PortUUID != plan.PortUUID || prior.SwitchUUID != plan.SwitchUUID || prior.NetworkID != p.NetworkID || prior.ProjectID != p.ProjectID || prior.InstanceUUID != p.InstanceUUID || prior.Device != p.Device || prior.Source != p.Source {
				return errors.New("Normal Add original producer identity changed")
			}
		}

		p.Version, p.RootUUID, p.SwitchUUID, p.PortUUID = 1, o.backendID, plan.SwitchUUID, plan.PortUUID
		err = p.valid(o.backendID)
		if err != nil {
			return err
		}

		immutable, ipamRow := nicAddImmutable(row), nicAddIPAMRow(switchRow)
		if attempt == 0 {
			original, originalIPAM = immutable, ipamRow
		} else if !reflect.DeepEqual(original, immutable) || !reflect.DeepEqual(originalIPAM, ipamRow) {
			return errors.New("Normal Add original port owner/settings or switch IPAM changed")
		}

		ipam, err := nicCleanupStringMap(switchRow["other_config"])
		if err != nil {
			return err
		}

		ready, err := nicAddAllocationReady(row["dynamic_addresses"], dynamic, opts.MAC.String(), ipam)
		if err != nil {
			return err
		}

		if ready {
			settings, _ := json.Marshal(publicationSettings(row))
			if attemptedSettings != nil && string(settings) != string(attemptedSettings) {
				return errors.New("Normal Add generated settings changed during publication")
			}

			attemptedSettings = settings
			p.Settings = publicationSettings(row)
			err = o.setNICAddPublication(ctx, plan, row, originalIPAM, p)
			if err == nil || !nicAddPortMoved(err) {
				return err
			}
			// northd and ovn-controller also write other derived columns of the new port, such as
			// up, asynchronously; the next snapshot still refuses any pinned, IPAM or settings change.
		}

		if attempt < 199 {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("Normal Add publication did not converge: %w", ctx.Err())
			case <-timer.C:
			}
		}
	}

	return errors.New("Normal Add publication did not converge within 200 reads")
}

// nicAddPortMoved reports that only the guard on the Add port row itself failed.
func nicAddPortMoved(err error) bool {
	if errors.Is(err, backendDB.ErrFenced) {
		return false
	}

	var timedOut *ovsdb.TimedOut
	if !errors.As(err, &timedOut) || timedOut.Operation() == nil {
		return false
	}

	op := timedOut.Operation()
	return op.Op == ovsdb.OperationWait && op.Table == "Logical_Switch_Port"
}

// nicPublicationRowWait guards a port's content for a publication write. Row versions are server-local
// (they differ between relays and change on a database server restart) and northd rewrites the up
// column as the port binds; neither is part of the producer.
func nicPublicationRowWait(row ovsdb.Row) ovsdb.Operation {
	content := maps.Clone(row)
	delete(content, "up")
	return nicMigrationRowWait("Logical_Switch_Port", content, false)
}

// setNICAddPublication retains the existing publication guards and adds atomic switch IPAM binding.
func (o *NB) setNICAddPublication(ctx context.Context, plan NICPortCleanup, row, ipamRow ovsdb.Row, p NICConfigPublication) error {
	encoded, err := publicationWire(p)
	if err != nil {
		return err
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return err
	}

	ids[nicConfigPublicationKey] = encoded
	ops := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicMigrationRowWait("Logical_Switch", ipamRow, false), nicPublicationRowWait(row), {Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: row["_uuid"]}}, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}}}
	_, err = o.nicCleanupTransact(ctx, ops...)
	if err != nil {
		// A lost reply can only acknowledge the same exact completed rooted Add snapshot.
		_, current, sw, readErr := o.nicAddSnapshot(ctx, plan.SwitchName, plan.PortName)
		if readErr != nil || current == nil || current["_uuid"] != row["_uuid"] || !reflect.DeepEqual(nicAddIPAMRow(sw), ipamRow) {
			return errors.Join(err, readErr)
		}

		expected := nicAddImmutable(row)
		expected["external_ids"] = nicCleanupStringMapWire(ids)
		currentIDs, readErr := nicCleanupStringMap(current["external_ids"])
		if readErr != nil || !maps.Equal(currentIDs, ids) || !reflect.DeepEqual(nicAddImmutable(current), expected) || !reflect.DeepEqual(publicationSettings(current), p.Settings) {
			return errors.Join(err, readErr)
		}

		_, readErr = o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicMigrationRowWait("Logical_Switch", ipamRow, false), nicPublicationRowWait(current))
		return readErr
	}

	return nil
}

// CheckNICConfig compares fresh selected replay inputs with actual rooted producer evidence.
func (o *NB) CheckNICConfig(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, target NICConfigPublication) (NICConfigPublication, error) {
	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return NICConfigPublication{}, err
	}

	if row == nil {
		return NICConfigPublication{}, errors.New("Selected NIC replay port is absent")
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return NICConfigPublication{}, err
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return p, fmt.Errorf("Selected NIC replay lacks producer evidence; retire/remove and re-add its original NIC: %w", err)
	}

	err = p.valid(o.backendID)
	if err != nil {
		return p, err
	}

	if p.Phase != "start" || p.PortUUID != plan.PortUUID || p.SwitchUUID != plan.SwitchUUID || p.NetworkID != target.NetworkID || p.ProjectID != target.ProjectID || p.InstanceUUID != target.InstanceUUID || p.Device != target.Device || p.Source != ids[ovnExtIDIncusLocation] || !nicInputsEqual(p.Input, target.Input) || !maps.Equal(p.ACLIDs, target.ACLIDs) {
		return p, errors.New("Selected NIC replay differs from its actual producer; retire/remove and re-add its original NIC")
	}
	// JSON normalizes RFC7047 values for durable comparison without claiming cross-database atomicity.
	want, _ := json.Marshal(p.Settings)
	actual, _ := json.Marshal(publicationSettings(row))
	if !reflect.DeepEqual(want, actual) {
		return p, errors.New("Selected NIC replay generated MAC/IP/port settings changed")
	}

	enabled, err := nicCleanupPortEnabled(row["enabled"])
	if err != nil || !enabled {
		return p, errors.New("Selected NIC replay is not an active Start outcome")
	}

	_, err = o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicPublicationRowWait(row))
	return p, err
}

// transferNICConfigPublication derives target evidence only from the exact original producer and transfer plan.
func transferNICConfigPublication(p *NICMigrationShared, row *NICMigrationSharedRow) error {
	before, err := nicCleanupStringMap(row.Before["external_ids"])
	if err != nil {
		return err
	}

	if before[nicConfigPublicationKey] == "" {
		after, err := nicCleanupStringMap(row.After["external_ids"])
		if err != nil {
			return err
		}

		if after[nicConfigPublicationKey] != "" {
			return errors.New("Migration cannot adopt an unevidenced NIC publication")
		}

		return nil
	}

	var original NICConfigPublication
	err = json.Unmarshal([]byte(before[nicConfigPublicationKey]), &original)
	if err != nil {
		return err
	}

	err = original.valid(p.Port.RootUUID)
	if err != nil {
		return err
	}

	if original.Phase != "start" || original.PortUUID != p.Port.PortUUID || original.SwitchUUID != p.Port.SwitchUUID || original.Source != p.Port.Source {
		return errors.New("Migration original NIC config publication changed")
	}

	var owner NICPrefixOwner
	err = json.Unmarshal([]byte(before[nicPrefixGeneration]), &owner)
	if err != nil || owner.Generation != original.Generation || owner.PortUUID != original.PortUUID || owner.RootUUID != original.RootUUID || owner.SwitchUUID != original.SwitchUUID || owner.Source != original.Source || owner.PortName != p.Port.PortName || owner.SwitchName != p.Port.SwitchName {
		return errors.New("Migration original config/allocation generations disagree")
	}

	want, _ := json.Marshal(original.Settings)
	actual, _ := json.Marshal(publicationSettings(row.Before))
	if !reflect.DeepEqual(want, actual) {
		return errors.New("Migration original NIC generated settings changed")
	}

	target := original
	target.Generation = p.TargetGeneration
	target.Operation = p.Operation
	target.Source = p.Target
	after, err := nicCleanupStringMap(row.After["external_ids"])
	if err != nil {
		return err
	}

	encoded, err := publicationWire(target)
	if err != nil {
		return err
	}

	after[nicConfigPublicationKey] = encoded
	row.After["external_ids"] = nicCleanupStringMapWire(after)
	return nil
}

func validateTransferredNICConfig(p NICMigrationShared, row NICMigrationSharedRow) error {
	expected := row
	expected.After = maps.Clone(row.After)
	err := transferNICConfigPublication(&p, &expected)
	if err != nil {
		return err
	}

	want, err := nicCleanupStringMap(expected.After["external_ids"])
	if err != nil {
		return err
	}

	actual, err := nicCleanupStringMap(row.After["external_ids"])
	if err != nil {
		return err
	}

	if want[nicConfigPublicationKey] != actual[nicConfigPublicationKey] {
		return errors.New("Migration target NIC config receipt differs from exact original/operation/generation")
	}

	return nil
}

// nicConfigHostOnlyInputs only affect the host side or guest presentation and are updated live
// without a backend effect, so they are not part of the recorded producer.
var nicConfigHostOnlyInputs = []string{"boot.priority", "connected", "limits.ingress.bucket", "limits.egress.bucket", "limits.max.bucket"}

// nicInputsEqual compares backend inputs, ignoring host-only keys recorded by earlier publications.
func nicInputsEqual(a, b map[string]string) bool {
	backend := func(input map[string]string) map[string]string {
		result := maps.Clone(input)
		for _, key := range nicConfigHostOnlyInputs {
			delete(result, key)
		}

		return result
	}

	return maps.Equal(backend(a), backend(b))
}

// NICConfigInputs retains backend device inputs, excluding host presentation and network-derived MTU.
func NICConfigInputs(config map[string]string) map[string]string {
	input := maps.Clone(config)
	for _, key := range append([]string{"host_name", "name", "mtu", "type", "nictype", "parent"}, nicConfigHostOnlyInputs...) {
		delete(input, key)
	}

	for key, value := range input {
		if value == "" {
			delete(input, key)
		}
	}

	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		value := input[key]
		if strings.Contains(value, "/") {
			input[key], _, _ = strings.Cut(value, "/")
		}
	}

	return input
}

func (s *physicalReferences) nicConfig(row ovsdb.Row, sw ovsdb.Row, target NICConfigPublication, active bool) (NICConfigPublication, error) {
	var p NICConfigPublication
	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return p, err
	}

	pid, err := nicCleanupRowUUID(row, "_uuid")
	if err != nil {
		return p, err
	}

	sid, err := nicCleanupRowUUID(sw, "_uuid")
	if err != nil {
		return p, err
	}

	swName, validName := sw["name"].(string)
	if !validName {
		return p, errors.New("NIC switch has an invalid name")
	}

	if nicLegacyPort(row, ids, swName, target) {
		// An upstream-created port has no recorded producer; its exact selected instance device is
		// taken as producer, as upstream reload and removal did, with its current backend settings.
		enabled, err := nicCleanupPortEnabled(row["enabled"])
		if err != nil {
			return p, err
		}

		p = target
		p.Version, p.RootUUID, p.SwitchUUID, p.PortUUID, p.Generation, p.Legacy = 1, s.root, sid, pid, pid, true
		p.Phase = "add"
		if enabled {
			p.Phase = "start"
		}

		p.Settings, err = publicationSettingsWire(row)
		if err != nil {
			return p, err
		}

		err = p.valid(s.root)
		if err != nil {
			return p, err
		}
	} else {
		err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
		if err != nil {
			return p, errors.New("NIC lacks original producer evidence; legacy replay/retirement is unsupported")
		}

		err = p.valid(s.root)
		if err != nil {
			return p, err
		}
	}

	ports, err := physicalUUIDs(sw["ports"])
	if err != nil {
		return p, err
	}

	enabled, err := nicCleanupPortEnabled(row["enabled"])
	if err != nil {
		return p, err
	}

	// A stopped instance moved to another member keeps its last producer until its NIC starts
	// there; the source only matters while the port is enabled.
	sourceMatches := p.Source == target.Source || !enabled
	if !ports[pid] || p.PortUUID != pid || p.SwitchUUID != sid || p.Phase == "pending" || p.NetworkID != target.NetworkID || p.ProjectID != target.ProjectID || p.InstanceUUID != target.InstanceUUID || p.Device != target.Device || !sourceMatches || !nicInputsEqual(p.Input, target.Input) || !maps.Equal(p.ACLIDs, target.ACLIDs) {
		return p, errors.New("NIC backend producer differs from the selected original configuration or placement")
	}

	if ids[ovnExtIDIncusSwitch] != sw["name"] || p.Phase == "start" && ids[ovnExtIDIncusLocation] != p.Source {
		return p, errors.New("NIC backend parent/source changed")
	}

	// Dynamic addresses are northd-owned and may legitimately be reassigned; the producer-written
	// address request and port security remain pinned.
	recorded := maps.Clone(p.Settings)
	current := publicationSettings(row)
	delete(recorded, "dynamic_addresses")
	delete(current, "dynamic_addresses")
	want, _ := json.Marshal(recorded)
	actual, _ := json.Marshal(current)
	if !reflect.DeepEqual(want, actual) {
		return p, errors.New("NIC generated backend settings changed")
	}

	if active && (p.Phase != "start" || !enabled) {
		return p, errors.New("Disabled or unstarted NIC is not an applicable shared reload target")
	}

	if p.Phase == "start" && (!p.Legacy || !nicPrefixLegacyMarker(ids)) {
		var owner NICPrefixOwner
		err = json.Unmarshal([]byte(ids[nicPrefixGeneration]), &owner)
		if err != nil || owner.Generation != p.Generation || owner.PortUUID != pid || owner.SwitchUUID != sid || owner.RootUUID != s.root || owner.Source != p.Source {
			return p, errors.New("NIC allocation/config producer generations disagree")
		}
	}

	return p, nil
}

// NICReplayProducer pins the original completed producer and its observed enabled state.
type NICReplayProducer struct {
	Publication NICConfigPublication
	Enabled     bool
	Location    string
}

func (s *physicalReferences) nicReplayProducer(row, sw ovsdb.Row, target NICConfigPublication) (NICReplayProducer, error) {
	p, err := s.nicConfig(row, sw, target, false)
	if err != nil {
		return NICReplayProducer{}, err
	}

	enabled, err := nicCleanupPortEnabled(row["enabled"])
	if err != nil {
		return NICReplayProducer{}, err
	}

	if p.Phase == "add" && enabled {
		return NICReplayProducer{}, errors.New("Unstarted Add producer unexpectedly enabled")
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return NICReplayProducer{}, err
	}

	return NICReplayProducer{Publication: p, Enabled: enabled, Location: ids[ovnExtIDIncusLocation]}, nil
}

// CheckNetworkNICReplay selects exact completed Add/Start consumers before reload.
func (o *NB) CheckNetworkNICReplay(ctx context.Context, networkID int64, routerPort string, targets map[OVNSwitchPort]NICConfigPublication) (map[OVNSwitchPort]NICReplayProducer, error) {
	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	referenceErr := s.networkPortsConsistent(networkID)
	if referenceErr != nil {
		return nil, referenceErr
	}

	swName := fmt.Sprintf("incus-net%d-ls-int", networkID)
	active := map[OVNSwitchPort]NICReplayProducer{}
	for _, sw := range s.rows["Logical_Switch"] {
		if sw["name"] != swName {
			continue
		}

		ports, err := physicalUUIDs(sw["ports"])
		if err != nil {
			return nil, err
		}

		for _, row := range s.rows["Logical_Switch_Port"] {
			id, err := nicCleanupRowUUID(row, "_uuid")
			if err != nil {
				return nil, err
			}

			if !ports[id] {
				continue
			}

			name, validName := row["name"].(string)
			if !validName {
				return nil, errors.New("Physical NIC has an invalid name")
			}

			if s.networkReloadInfrastructure(row, swName, routerPort) {
				continue
			}

			target, ok := targets[OVNSwitchPort(name)]
			if !ok {
				return nil, fmt.Errorf("Physical NIC %q is absent from current instance candidates", name)
			}

			producer, err := s.nicReplayProducer(row, sw, target)
			if err != nil {
				return nil, fmt.Errorf("Physical NIC %q cannot be reloaded: %w", name, err)
			}

			active[OVNSwitchPort(name)] = producer
		}
	}

	_, err = o.nicCleanupTransact(ctx, s.waits()...)
	return active, err
}

// CompleteNICConfigReload publishes only the same original producer after its enclosing shared reload.
func (o *NB) CompleteNICConfigReload(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, target NICConfigPublication) error {
	_, _, p, err := o.nicConfigReloadPublication(ctx, sw, port, target)
	if err != nil {
		return err
	}

	return o.PublishNICConfig(ctx, sw, port, p)
}

func (o *NB) nicConfigReloadPublication(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, target NICConfigPublication) (NICPortCleanup, ovsdb.Row, NICConfigPublication, error) {
	guarded, ok := o.client.(*referenceMutationClient)
	if !ok {
		return NICPortCleanup{}, nil, NICConfigPublication{}, errors.New("Shared reload completion lacks enclosing producer guard")
	}

	original, found := guarded.replay[port]
	if !found || target.Phase != original.Publication.Phase {
		return NICPortCleanup{}, nil, NICConfigPublication{}, errors.New("Shared reload completion differs from captured original phase")
	}

	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil {
		return NICPortCleanup{}, nil, NICConfigPublication{}, err
	}

	if row == nil {
		return NICPortCleanup{}, nil, NICConfigPublication{}, errors.New("Shared reload original port disappeared")
	}

	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return NICPortCleanup{}, nil, NICConfigPublication{}, err
	}

	var p NICConfigPublication
	err = json.Unmarshal([]byte(ids[nicConfigPublicationKey]), &p)
	if err != nil {
		return NICPortCleanup{}, nil, NICConfigPublication{}, err
	}

	err = p.valid(o.backendID)
	if err != nil {
		return NICPortCleanup{}, nil, NICConfigPublication{}, err
	}

	if p.Phase != "pending" || p.PortUUID != plan.PortUUID || p.SwitchUUID != plan.SwitchUUID || p.NetworkID != target.NetworkID || p.ProjectID != target.ProjectID || p.InstanceUUID != target.InstanceUUID || p.Device != target.Device || (p.Source != target.Source && original.Enabled) || !nicInputsEqual(p.Input, target.Input) {
		return NICPortCleanup{}, nil, NICConfigPublication{}, errors.New("Shared reload original producer changed")
	}

	if target.Phase != "add" && target.Phase != "start" {
		return NICPortCleanup{}, nil, NICConfigPublication{}, errors.New("Shared reload completion lacks captured original phase")
	}

	p.Phase = target.Phase
	p.ACLIDs = maps.Clone(target.ACLIDs)
	return plan, row, p, nil
}

func decodeNICConfig(wire string) (NICConfigPublication, error) {
	var p NICConfigPublication
	err := json.Unmarshal([]byte(wire), &p)
	return p, err
}
