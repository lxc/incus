package ovn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

const (
	nicPrefixMetadata   = "incus.nic-prefix-owners.v1"
	nicPrefixGeneration = "incus.nic-prefix-generation.v1"
)

// NICPrefixOwner identifies one allocation, independently of port names or Desired.
type NICPrefixOwner struct {
	RootUUID    string
	SwitchUUID  string
	SwitchName  OVNSwitch
	PortUUID    string
	PortName    OVNSwitchPort
	Generation  string
	PortVersion string
	// StartContent pins the producer snapshot for a normal Start's first publication.
	// Older and cleanup/migration owners retain their strict PortVersion guard.
	StartContent string `json:",omitempty"`
	Source       string
	Previous     string
	// Legacy marks an owner that adopted the existing prefixes of an upstream-created port.
	Legacy bool `json:",omitempty"`
}

// ErrNICPrefixLegacy reports an upstream-created port whose shared prefixes predate ownership tracking.
var ErrNICPrefixLegacy = errors.New("NIC prefix provenance predates ownership tracking")

// nicPrefixLegacyMarker reports a port without allocation provenance, or whose only provenance is
// the adoption of its upstream-created prefixes.
func nicPrefixLegacyMarker(external map[string]string) bool {
	if external[nicPrefixGeneration] == "" {
		return true
	}

	var owner NICPrefixOwner
	return json.Unmarshal([]byte(external[nicPrefixGeneration]), &owner) == nil && owner.Legacy
}

// nicPrefixLegacy reports an upstream-created port with neither allocation marker nor a recorded
// producer other than one adopted from upstream.
func nicPrefixLegacy(external map[string]string) bool {
	if external[nicPrefixGeneration] != "" {
		return false
	}

	if external[nicConfigPublicationKey] == "" {
		return true
	}

	var p NICConfigPublication
	return json.Unmarshal([]byte(external[nicConfigPublicationKey]), &p) == nil && p.Legacy
}

// Validate checks the complete original prefix owner identity.
func (owner NICPrefixOwner) Validate(root string, port NICPortCleanup) error {
	if owner.StartContent != "" {
		content, err := hex.DecodeString(owner.StartContent)
		if err != nil || len(content) != sha256.Size {
			return errors.New("NIC Start producer content identity is invalid")
		}
	}

	if owner.SwitchName == "" || owner.PortName == "" || owner.Source == "" || port.Source != "" && port.Source != owner.Source || len(owner.Previous) != 64 {
		return errors.New("NIC prefix allocation names or previous marker are invalid")
	}

	if owner.RootUUID != root || owner.RootUUID != port.RootUUID || owner.SwitchUUID != port.SwitchUUID || owner.SwitchName != port.SwitchName || owner.PortUUID != port.PortUUID || owner.PortName != port.PortName {
		return errors.New("NIC prefix allocation disagrees with original port/root")
	}

	for _, id := range []string{root, owner.SwitchUUID, owner.PortUUID, owner.Generation, owner.PortVersion} {
		if !nicCleanupUUID(id) {
			return errors.New("NIC prefix allocation identity is invalid")
		}
	}

	return nil
}

func (owner NICPrefixOwner) port() NICPortCleanup {
	return NICPortCleanup{RootUUID: owner.RootUUID, SwitchUUID: owner.SwitchUUID, SwitchName: owner.SwitchName, PortUUID: owner.PortUUID, PortName: owner.PortName, Source: owner.Source}
}

func nicPrefixEncode(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// nicPrefixTransferred reports whether the port's producer publication names source.
func nicPrefixTransferred(external map[string]string, source string) bool {
	var p NICConfigPublication
	return external[nicConfigPublicationKey] != "" && json.Unmarshal([]byte(external[nicConfigPublicationKey]), &p) == nil && p.Source == source
}

// NewNICPrefixOwner captures a fresh Start generation without publishing an effect.
func (o *NB) NewNICPrefixOwner(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, source string) (NICPrefixOwner, error) {
	captured, err := o.CaptureNICPortCleanup(ctx, sw, port, source)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: captured.PortUUID})
	if err != nil || len(rows) != 1 {
		return NICPrefixOwner{}, errors.Join(err, errors.New("NIC allocation port is absent or ambiguous"))
	}

	version, err := nicCleanupRowUUID(rows[0], "_version")
	if err != nil || version != captured.PortVersion {
		return NICPrefixOwner{}, errors.New("NIC allocation port changed during capture")
	}

	external, err := nicCleanupStringMap(rows[0]["external_ids"])
	if err != nil {
		return NICPrefixOwner{}, err
	}

	owner := NICPrefixOwner{RootUUID: o.backendID, SwitchUUID: captured.SwitchUUID, SwitchName: sw, PortUUID: captured.PortUUID, PortName: port, Generation: uuid.NewString(), PortVersion: version, Source: source, Previous: nicPrefixDigest(external[nicPrefixGeneration])}
	return owner, owner.Validate(o.backendID, captured)
}

// CaptureNICPrefixOwner requires the actual allocation marker on the original row.
func (o *NB) CaptureNICPrefixOwner(ctx context.Context, port NICPortCleanup) (NICPrefixOwner, error) {
	owner, _, err := o.CaptureNICPrefixOwnerTransferred(ctx, port)
	return owner, err
}

// CaptureNICPrefixOwnerTransferred also reports whether the captured owner is the previous member's
// generation retained on a port whose producer was transferred to this member by a cold move.
func (o *NB) CaptureNICPrefixOwnerTransferred(ctx context.Context, port NICPortCleanup) (NICPrefixOwner, bool, error) {
	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: port.PortUUID})
	if err != nil || len(rows) != 1 {
		return NICPrefixOwner{}, false, errors.Join(err, errors.New("Original NIC allocation marker is unavailable"))
	}

	external, err := nicCleanupStringMap(rows[0]["external_ids"])
	if err != nil {
		return NICPrefixOwner{}, false, err
	}

	if nicPrefixLegacy(external) {
		return NICPrefixOwner{}, false, ErrNICPrefixLegacy
	}

	var owner NICPrefixOwner
	err = json.Unmarshal([]byte(external[nicPrefixGeneration]), &owner)
	if err != nil {
		return NICPrefixOwner{}, false, errors.New("NIC prefix provenance is unknown; preserving shared contributions")
	}

	// After a cold move the marker may still be the previous member's, whose cleanup the producer
	// transfer required to be acknowledged; this member has not published its own generation yet.
	checkPort := port
	transferred := port.Source != "" && owner.Source != port.Source && nicPrefixTransferred(external, port.Source)
	if transferred {
		checkPort.Source = ""
	}

	err = owner.Validate(o.backendID, checkPort)
	if err != nil {
		return NICPrefixOwner{}, false, err
	}

	if rows[0]["name"] != string(port.PortName) || (!transferred && external[ovnExtIDIncusLocation] != owner.Source) {
		return NICPrefixOwner{}, false, errors.New("Original NIC allocation port was renamed")
	}

	_, err = o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(port), nicCleanupPortOwnerWait(port), nicPrefixRowWait("Logical_Switch_Port", rows[0]), nicCleanupRootWait(o.backendID))
	return owner, transferred, err
}

type nicPrefixContribution struct {
	Owner      NICPrefixOwner
	Prefixes   []string
	Superseded string
}

// Released receipts prove retirement even when a sibling preserves an equal field.
type nicPrefixLedger struct {
	Version  int
	RootUUID string
	RowUUID  string
	Baseline []string
	Managed  []string
	Owners   map[string]nicPrefixContribution
	Released map[string]nicPrefixContribution
}

// NICPrefixCleanup is stored in the original stopped-source snapshot.
type NICPrefixCleanup struct {
	Owner      NICPrefixOwner
	Prefixes   []string
	Superseded string
}

func nicPrefixDigest(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func nicPrefixCanonical(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func (ledger nicPrefixLedger) addresses() []string {
	values := append(slices.Clone(ledger.Baseline), ledger.Managed...)
	for _, contribution := range ledger.Owners {
		values = append(values, contribution.Prefixes...)
	}

	return nicPrefixCanonical(values)
}

func nicPrefixValues(table string, row ovsdb.Row) ([]string, error) {
	if table == "Address_Set" {
		return nicCleanupStringSet(row["addresses"])
	}

	options, err := nicCleanupStringMap(row["options"])
	if err != nil {
		return nil, err
	}

	entries := strings.Fields(options["arp_proxy"])
	if len(nicPrefixCanonical(entries)) != len(entries) {
		return nil, errors.New("Duplicate shared proxy prefix")
	}

	return nicPrefixCanonical(entries), nil
}

func (o *NB) nicPrefixLedger(table string, row ovsdb.Row, initialize bool) (nicPrefixLedger, map[string]string, error) {
	id, err := nicCleanupRowUUID(row, "_uuid")
	if err != nil {
		return nicPrefixLedger{}, nil, err
	}

	external, err := nicCleanupStringMap(row["external_ids"])
	if err != nil {
		return nicPrefixLedger{}, nil, err
	}

	current, err := nicPrefixValues(table, row)
	if err != nil {
		return nicPrefixLedger{}, nil, err
	}

	ledger := nicPrefixLedger{}
	if external[nicPrefixMetadata] == "" {
		if !initialize {
			return ledger, nil, errors.New("Shared prefix provenance is unknown; preserving original contributions")
		}

		ledger = nicPrefixLedger{Version: 1, RootUUID: o.backendID, RowUUID: id, Baseline: current, Owners: map[string]nicPrefixContribution{}, Released: map[string]nicPrefixContribution{}}
	} else {
		err = json.Unmarshal([]byte(external[nicPrefixMetadata]), &ledger)
		if err != nil || ledger.Version != 1 || ledger.RootUUID != o.backendID || ledger.RowUUID != id || ledger.Owners == nil || ledger.Released == nil || !slices.Equal(ledger.Baseline, nicPrefixCanonical(ledger.Baseline)) || !slices.Equal(ledger.Managed, nicPrefixCanonical(ledger.Managed)) {
			return ledger, nil, errors.New("Shared prefix ownership identity is invalid or replaced")
		}

		for generation, contribution := range ledger.Owners {
			if generation != contribution.Owner.Generation || contribution.Owner.Validate(o.backendID, contribution.Owner.port()) != nil || !slices.Equal(contribution.Prefixes, nicPrefixCanonical(contribution.Prefixes)) || contribution.Superseded != "" && !nicCleanupUUID(contribution.Superseded) {
				return ledger, nil, errors.New("Shared prefix contributor identity is invalid")
			}

			{
				_, retired := ledger.Released[generation]
				if retired {
					return ledger, nil, errors.New("Shared prefix generation is both active and retired")
				}
			}
		}

		for generation, contribution := range ledger.Released {
			if generation != contribution.Owner.Generation || contribution.Owner.Validate(o.backendID, contribution.Owner.port()) != nil || !slices.Equal(contribution.Prefixes, nicPrefixCanonical(contribution.Prefixes)) || contribution.Superseded != "" && !nicCleanupUUID(contribution.Superseded) {
				return ledger, nil, errors.New("Shared prefix retirement receipt is invalid")
			}
		}

		if !slices.Equal(current, ledger.addresses()) {
			return ledger, nil, errors.New("Shared prefix field changed outside ownership protocol; retaining cleanup debt")
		}
	}

	return ledger, external, nil
}

func nicPrefixColumns(table string) []string {
	columns := []string{"_uuid", "_version", "name", "external_ids"}
	if table == "Address_Set" {
		return append(columns, "addresses")
	}

	return append(columns, "type", "options")
}

func (o *NB) nicPrefixRead(ctx context.Context, table string, column string, value any) ([]ovsdb.Row, error) {
	schema, exists := o.client.Schema().Tables[table]
	if !exists {
		return nil, errors.New("Shared prefix table schema is unavailable")
	}

	for _, required := range nicPrefixColumns(table)[2:] {
		_, exists := schema.Columns[required]
		if !exists {
			return nil, fmt.Errorf("Shared prefix schema is missing %q", required)
		}
	}

	columns := []string{"_uuid", "_version"}
	for name := range schema.Columns {
		columns = append(columns, name)
	}

	slices.Sort(columns)
	results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: table, Where: []ovsdb.Condition{{Column: column, Function: ovsdb.ConditionEqual, Value: value}}, Columns: columns})
	if err != nil {
		return nil, err
	}

	for _, row := range results[1].Rows {
		for _, name := range columns {
			_, exists := row[name]
			if !exists {
				return nil, fmt.Errorf("Shared prefix snapshot is missing %q", name)
			}
		}
	}

	return results[1].Rows, nil
}

// Forwarded guards compare content because _version belongs to the serving server.
func nicPrefixRowWait(table string, row ovsdb.Row) ovsdb.Operation {
	zero := 0
	columns := []string{}
	for column := range row {
		if column != "_version" {
			columns = append(columns, column)
		}
	}

	slices.Sort(columns)
	where := []ovsdb.Condition{}
	for _, column := range columns {
		where = append(where, ovsdb.Condition{Column: column, Function: ovsdb.ConditionEqual, Value: row[column]})
	}

	return ovsdb.Operation{Op: ovsdb.OperationWait, Table: table, Timeout: &zero, Where: where, Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": row["_uuid"]}}}
}

func nicPrefixUpdate(table string, row ovsdb.Row, ledger nicPrefixLedger, external map[string]string) ovsdb.Operation {
	metadata := nicPrefixEncode(ledger)
	if external[nicPrefixMetadata] == metadata {
		return nicPrefixRowWait(table, row)
	}

	external = maps.Clone(external)
	external[nicPrefixMetadata] = metadata
	update := ovsdb.Row{"external_ids": nicCleanupStringMapWire(external)}
	values := ledger.addresses()
	if table == "Address_Set" {
		update["addresses"] = nicCleanupStringSetWire(values)
	} else {
		options, _ := nicCleanupStringMap(row["options"])
		if len(values) == 0 {
			delete(options, "arp_proxy")
		} else {
			options["arp_proxy"] = strings.Join(values, " ")
		}

		update["options"] = nicCleanupStringMapWire(options)
	}

	return ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: table, Where: nicPrefixRowWait(table, row).Where, Row: update}
}

func (o *NB) nicPrefixCommit(ctx context.Context, operations []ovsdb.Operation) error {
	results, err := o.nicCleanupTransact(ctx, append(operations, nicCleanupRootWait(o.backendID))...)
	if err != nil {
		return err
	}

	for i, op := range operations {
		if op.Op == ovsdb.OperationUpdate && results[i].Count != 1 {
			return errors.New("NIC shared-prefix transaction was not acknowledged")
		}
	}

	return nil
}

func nicPrefixRequested(addresses []net.IPNet, family int) []string {
	values := []string{}
	for _, address := range addresses {
		v := 6
		if address.IP.To4() != nil {
			v = 4
		}

		if family == 0 || family == v {
			values = append(values, address.String())
		}
	}

	return nicPrefixCanonical(values)
}

// nicPrefixPublish atomically publishes ownership, its NIC marker and shared effect.
type nicPrefixPublication struct {
	Table    string
	Row      ovsdb.Row
	Prefixes []string
	Guards   []ovsdb.Operation
	// Adopt takes only the requested prefixes the row holds as unowned baseline.
	Adopt bool
}

func (o *NB) nicPrefixPublishOperations(ctx context.Context, publications []nicPrefixPublication, owner NICPrefixOwner, target string, retain []string) ([]ovsdb.Operation, error) {
	err := owner.Validate(o.backendID, owner.port())
	if err != nil {
		return nil, err
	}

	portRows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: owner.PortUUID})
	if err != nil || len(portRows) != 1 {
		return nil, errors.Join(err, errors.New("NIC allocation source port is missing"))
	}

	portRow := portRows[0]
	external, err := nicCleanupStringMap(portRow["external_ids"])
	if err != nil || portRow["name"] != string(owner.PortName) || external[ovnExtIDIncusLocation] != owner.Source {
		return nil, errors.New("NIC allocation source port was changed")
	}

	portExternal := maps.Clone(external)

	originalPort := owner.port()
	if target != "" {
		owner.Source = target
	}

	marker := nicPrefixEncode(owner)
	{
		previous := external[nicPrefixGeneration]
		if previous != "" && previous != marker {
			var original NICPrefixOwner
			if json.Unmarshal([]byte(previous), &original) != nil {
				return nil, errors.New("NIC previous allocation marker is invalid")
			}

			previousPort := originalPort
			if original.Source != owner.Source && nicPrefixTransferred(external, owner.Source) {
				// A cold move already handed the producer to this member after its previous member
				// acknowledged cleanup; the earlier marker keeps its exact port/root identity.
				previousPort.Source = ""
			}

			if original.Validate(o.backendID, previousPort) != nil {
				return nil, errors.New("NIC previous allocation marker is invalid")
			}
		}
	}

	version, err := nicCleanupRowUUID(portRow, "_version")
	if err != nil {
		return nil, err
	}

	portChanged := version != owner.PortVersion
	portGuard := nicPrefixRowWait("Logical_Switch_Port", portRow)
	if owner.StartContent != "" {
		content, err := nicStartPortDigest(portRow)
		if err != nil {
			return nil, err
		}

		portChanged = content != owner.StartContent
		portGuard = nicPublicationRowWait(portRow)
	}

	if external[nicPrefixGeneration] != marker && (nicPrefixDigest(external[nicPrefixGeneration]) != owner.Previous || portChanged) {
		return nil, errors.New("NIC allocation generation changed before publication")
	}

	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(originalPort), nicCleanupPortOwnerWait(originalPort), portGuard}

	external[nicPrefixGeneration] = marker
	if target != "" {
		external[ovnExtIDIncusLocation] = target
	}

	operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: portGuard.Where, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(external)}})
	livePorts, err := o.nicPrefixLivePorts(ctx)
	if err != nil {
		return nil, err
	}

	// An upstream-created port's existing entries become part of its first tracked allocation, so a
	// republish before its first stop does not leave them behind.
	legacy := nicPrefixLegacy(portExternal)

	for _, publication := range publications {
		table, row := publication.Table, publication.Row
		operations = append(operations, publication.Guards...)
		ledger, ids, err := o.nicPrefixLedger(table, row, true)
		if err != nil {
			return nil, err
		}

		// Receipts serve the replay of a stop that has not completed, a rollback of this allocation
		// (which proves the previous one) and a live-migration source cleanup that still has to prove
		// the retirement of its target generation (retain). A new allocation of this port follows its
		// own completed stops and a retired port was stopped first, so their other receipts are
		// dropped to keep the shared rows bounded.
		for generation, receipt := range ledger.Released {
			previous := nicPrefixDigest(nicPrefixEncode(receipt.Owner)) == owner.Previous
			if generation != owner.Generation && !previous && !slices.Contains(retain, generation) && (receipt.Owner.PortUUID == owner.PortUUID || !livePorts[receipt.Owner.PortUUID]) {
				delete(ledger.Released, generation)
			}
		}

		prefixes := publication.Prefixes
		if legacy && !publication.Adopt {
			for _, prefix := range prefixes {
				ledger.Baseline = slices.DeleteFunc(ledger.Baseline, func(value string) bool { return value == prefix })
			}
		}

		if publication.Adopt {
			prefixes = []string{}
			for _, prefix := range publication.Prefixes {
				if slices.Contains(ledger.Baseline, prefix) {
					prefixes = append(prefixes, prefix)
					ledger.Baseline = slices.DeleteFunc(ledger.Baseline, func(value string) bool { return value == prefix })
				}
			}
		}

		contribution := nicPrefixContribution{Owner: owner, Prefixes: prefixes}
		for generation, previous := range ledger.Owners {
			if generation == owner.Generation || previous.Owner.PortUUID != owner.PortUUID {
				continue
			}

			if nicPrefixDigest(nicPrefixEncode(previous.Owner)) != owner.Previous {
				return nil, errors.New("NIC previous allocation contribution disagrees with its marker")
			}

			contribution.Superseded = generation
			delete(ledger.Owners, generation)
			ledger.Released[generation] = previous
		}

		{
			existing, ok := ledger.Owners[owner.Generation]
			if ok {
				contribution.Superseded = existing.Superseded
			}
		}

		{
			_, retired := ledger.Released[owner.Generation]
			if retired {
				return nil, errors.New("Cannot republish retired NIC prefix allocation")
			}
		}

		{
			previous, exists := ledger.Owners[owner.Generation]
			if exists && nicPrefixEncode(previous) != nicPrefixEncode(contribution) {
				return nil, errors.New("NIC allocation contribution changed during retry")
			}
		}

		ledger.Owners[owner.Generation] = contribution
		operations = append(operations, nicPrefixRowWait(table, row), nicPrefixUpdate(table, row, ledger, ids))
	}

	return operations, nil
}

// nicPrefixLivePorts returns the identities of all current logical switch ports.
func (o *NB) nicPrefixLivePorts(ctx context.Context) (map[string]bool, error) {
	results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{}, Columns: []string{"_uuid"}})
	if err != nil {
		return nil, err
	}

	ports := make(map[string]bool, len(results[1].Rows))
	for _, row := range results[1].Rows {
		id, err := nicCleanupRowUUID(row, "_uuid")
		if err != nil {
			return nil, err
		}

		ports[id] = true
	}

	return ports, nil
}

func (o *NB) nicPrefixPublish(ctx context.Context, publications []nicPrefixPublication, owner NICPrefixOwner, retain []string) error {
	operations, err := o.nicPrefixPublishOperations(ctx, publications, owner, "", retain)
	if err != nil {
		return err
	}

	return o.nicPrefixCommit(ctx, operations)
}

// AdoptLegacyNICPrefixes gives an upstream-created port an allocation owning the requested shared
// prefixes the rows hold as unowned baseline, so that its Stop releases them as upstream did. The
// shared fields are unchanged; network-managed entries and other contributions stay in place.
func (o *NB) AdoptLegacyNICPrefixes(ctx context.Context, port NICPortCleanup, prefix OVNAddressSet, addresses []net.IPNet, proxySwitch OVNSwitch, proxyPort OVNSwitchPort, proxy []net.IPNet) (NICPrefixOwner, error) {
	owner, err := o.NewNICPrefixOwner(ctx, port.SwitchName, port.PortName, port.Source)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	if owner.PortUUID != port.PortUUID || owner.SwitchUUID != port.SwitchUUID || owner.Previous != nicPrefixDigest("") {
		return NICPrefixOwner{}, errors.New("Upstream NIC port changed during prefix adoption")
	}

	owner.Legacy = true
	publications, err := o.nicPrefixPublications(ctx, prefix, addresses, proxySwitch, proxyPort, proxy)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	for i := range publications {
		publications[i].Adopt = true
	}

	operations, err := o.nicPrefixPublishOperations(ctx, publications, owner, "", nil)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	return owner, o.nicPrefixCommit(ctx, operations)
}

// PublishNICPrefixes atomically installs both shared effects and their provenance. Receipts of the
// retained generations are kept for pending live-migration source cleanup.
func (o *NB) PublishNICPrefixes(ctx context.Context, prefix OVNAddressSet, addresses []net.IPNet, sw OVNSwitch, port OVNSwitchPort, proxy []net.IPNet, owner NICPrefixOwner, retain ...string) error {
	publications, err := o.nicPrefixPublications(ctx, prefix, addresses, sw, port, proxy)
	if err != nil {
		return err
	}

	return o.nicPrefixPublish(ctx, publications, owner, retain)
}

func (o *NB) nicPrefixPublications(ctx context.Context, prefix OVNAddressSet, addresses []net.IPNet, sw OVNSwitch, port OVNSwitchPort, proxy []net.IPNet) ([]nicPrefixPublication, error) {
	publications := []nicPrefixPublication{}
	for _, family := range []int{4, 6} {
		selected, err := o.nicPrefixRead(ctx, "Address_Set", "name", fmt.Sprintf("%s_ip%d", prefix, family))
		if err != nil || len(selected) != 1 {
			return nil, errors.Join(err, errors.New("Original shared address set is absent or ambiguous"))
		}

		publications = append(publications, nicPrefixPublication{Table: "Address_Set", Row: selected[0], Prefixes: nicPrefixRequested(addresses, family)})
	}

	if port != "" {
		identity, err := o.nicPrefixProxyIdentity(ctx, sw, port)
		if err != nil {
			return nil, err
		}

		selected, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: identity.PortUUID})
		if err != nil || len(selected) != 1 {
			return nil, errors.Join(err, errors.New("Original shared proxy port is unavailable"))
		}

		publications = append(publications, nicPrefixPublication{Table: "Logical_Switch_Port", Row: selected[0], Prefixes: nicPrefixRequested(proxy, 0), Guards: []ovsdb.Operation{nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity)}})
	}

	return publications, nil
}

// RollbackNICPrefixes uses the same allocation for uncertain replies and partial undo.
func (o *NB) RollbackNICPrefixes(ctx context.Context, prefix OVNAddressSet, addresses []net.IPNet, sw OVNSwitch, port OVNSwitchPort, proxy []net.IPNet, owner NICPrefixOwner) error {
	publications, err := o.nicPrefixPublications(ctx, prefix, addresses, sw, port, proxy)
	if err != nil {
		return err
	}

	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: owner.PortUUID})
	if err != nil || len(rows) != 1 {
		return errors.Join(err, errors.New("NIC rollback source port is unavailable; retaining uncertainty"))
	}

	row := rows[0]
	ids, err := nicCleanupStringMap(row["external_ids"])
	if err != nil || row["name"] != string(owner.PortName) || ids[ovnExtIDIncusLocation] != owner.Source {
		return errors.New("NIC rollback source port changed")
	}

	marker := ids[nicPrefixGeneration]
	published := marker == nicPrefixEncode(owner)
	if !published && nicPrefixDigest(marker) != owner.Previous {
		return errors.New("NIC rollback generation changed")
	}

	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(owner.port()), nicCleanupPortOwnerWait(owner.port()), nicPrefixRowWait("Logical_Switch_Port", row)}
	previousMarker := ""
	for _, publication := range publications {
		operations = append(operations, publication.Guards...)
		ledger, external, err := o.nicPrefixLedger(publication.Table, publication.Row, published)
		if !published {
			// An unchanged prior marker and guarded absent contribution prove no publication.
			if err == nil {
				{
					_, exists := ledger.Owners[owner.Generation]
					if exists {
						return errors.New("NIC unpublished marker has an active contribution")
					}
				}

				{
					retired, exists := ledger.Released[owner.Generation]
					if exists {
						if retired.Owner != owner || !slices.Equal(retired.Prefixes, publication.Prefixes) {
							return errors.New("NIC rollback receipt changed")
						}

						if retired.Superseded != "" {
							previous, exists := ledger.Owners[retired.Superseded]
							if !exists || nicPrefixEncode(previous.Owner) != marker {
								return errors.New("NIC rollback restored predecessor is unavailable")
							}
						}
					}
				}
			} else {
				external, mapErr := nicCleanupStringMap(publication.Row["external_ids"])
				if mapErr != nil || external[nicPrefixMetadata] != "" {
					return err
				}
			}

			operations = append(operations, nicPrefixRowWait(publication.Table, publication.Row))
			continue
		}

		if err != nil {
			return err
		}

		for _, previous := range ledger.Released {
			encoded := nicPrefixEncode(previous.Owner)
			if nicPrefixDigest(encoded) == owner.Previous {
				if previousMarker != "" && previousMarker != encoded {
					return errors.New("NIC rollback prior markers disagree")
				}

				previousMarker = encoded
			}
		}

		contribution, exists := ledger.Owners[owner.Generation]
		if !exists || contribution.Owner != owner || !slices.Equal(contribution.Prefixes, publication.Prefixes) {
			return errors.New("NIC rollback contribution changed or was already retired")
		}

		delete(ledger.Owners, owner.Generation)
		ledger.Released[owner.Generation] = contribution
		if contribution.Superseded != "" {
			previous, exists := ledger.Released[contribution.Superseded]
			if !exists || nicPrefixDigest(nicPrefixEncode(previous.Owner)) != owner.Previous {
				return errors.New("NIC rollback previous contribution is unavailable")
			}

			encoded := nicPrefixEncode(previous.Owner)
			if previousMarker != "" && previousMarker != encoded {
				return errors.New("NIC rollback previous identities disagree")
			}

			previousMarker = encoded
			delete(ledger.Released, contribution.Superseded)
			ledger.Owners[contribution.Superseded] = previous
		}

		operations = append(operations, nicPrefixRowWait(publication.Table, publication.Row), nicPrefixUpdate(publication.Table, publication.Row, ledger, external))
	}

	if published {
		if previousMarker == "" && owner.Previous != nicPrefixDigest("") {
			return errors.New("NIC rollback previous marker cannot be proven")
		}

		if previousMarker == "" {
			delete(ids, nicPrefixGeneration)
		} else {
			ids[nicPrefixGeneration] = previousMarker
		}

		operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: nicPrefixRowWait("Logical_Switch_Port", row).Where, Row: ovsdb.Row{"external_ids": nicCleanupStringMapWire(ids)}})
	}

	return o.nicPrefixCommit(ctx, operations)
}

func (o *NB) nicPrefixProxyIdentity(ctx context.Context, sw OVNSwitch, port OVNSwitchPort) (NICPortCleanup, error) {
	results, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(sw)}}, Columns: []string{"_uuid"}}, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: string(port)}}, Columns: []string{"_uuid", "type", "options"}})
	if err != nil || len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		return NICPortCleanup{}, errors.Join(err, errors.New("Shared proxy parent/port is absent or ambiguous"))
	}

	swID, err := nicCleanupRowUUID(results[1].Rows[0], "_uuid")
	if err != nil {
		return NICPortCleanup{}, err
	}

	portID, err := nicCleanupRowUUID(results[2].Rows[0], "_uuid")
	if err != nil {
		return NICPortCleanup{}, err
	}

	options, err := nicCleanupStringMap(results[2].Rows[0]["options"])
	if err != nil || results[2].Rows[0]["type"] != "router" || options["router-port"] == "" {
		return NICPortCleanup{}, errors.New("Shared proxy port was repurposed")
	}

	return NICPortCleanup{SwitchUUID: swID, SwitchName: sw, PortUUID: portID, PortName: port}, nil
}

func (o *NB) nicPrefixCapture(table string, row ovsdb.Row, addresses []net.IPNet, owner NICPrefixOwner) (NICPrefixCleanup, error) {
	ledger, _, err := o.nicPrefixLedger(table, row, false)
	if err != nil {
		return NICPrefixCleanup{}, err
	}

	contribution, ok := ledger.Owners[owner.Generation]
	if !ok {
		contribution, ok = ledger.Released[owner.Generation]
	}

	if !ok || nicPrefixEncode(contribution.Owner) != nicPrefixEncode(owner) {
		return NICPrefixCleanup{}, errors.New("Original NIC prefix allocation is unknown or retired")
	}

	requested := nicPrefixRequested(addresses, 0)
	for _, prefix := range contribution.Prefixes {
		if !slices.Contains(requested, prefix) {
			return NICPrefixCleanup{}, errors.New("Original NIC allocation differs from stopped source prefixes")
		}
	}

	return NICPrefixCleanup{Owner: owner, Prefixes: contribution.Prefixes, Superseded: contribution.Superseded}, nil
}

func (proof NICPrefixCleanup) validate(root string, addresses []net.IPNet) error {
	err := proof.Owner.Validate(root, proof.Owner.port())
	if err != nil || !slices.Equal(proof.Prefixes, nicPrefixCanonical(proof.Prefixes)) || proof.Superseded != "" && !nicCleanupUUID(proof.Superseded) {
		return errors.New("Original NIC prefix cleanup proof is invalid")
	}

	for _, prefix := range proof.Prefixes {
		if !slices.Contains(nicPrefixRequested(addresses, 0), prefix) {
			return errors.New("Original NIC prefix cleanup differs from source request")
		}
	}

	return nil
}

func (o *NB) nicPrefixRelease(table string, row ovsdb.Row, proof NICPrefixCleanup) (ovsdb.Operation, error) {
	ledger, external, err := o.nicPrefixLedger(table, row, false)
	if err != nil {
		return ovsdb.Operation{}, err
	}

	want := nicPrefixContribution(proof)
	{
		retired, ok := ledger.Released[proof.Owner.Generation]
		if ok {
			if nicPrefixEncode(retired) != nicPrefixEncode(want) {
				return ovsdb.Operation{}, errors.New("Original NIC retirement receipt changed")
			}

			return nicPrefixRowWait(table, row), nil
		}
	}

	active, ok := ledger.Owners[proof.Owner.Generation]
	if !ok || nicPrefixEncode(active) != nicPrefixEncode(want) {
		return ovsdb.Operation{}, errors.New("Original NIC contribution changed or disappeared without receipt")
	}

	delete(ledger.Owners, proof.Owner.Generation)
	ledger.Released[proof.Owner.Generation] = want
	return nicPrefixUpdate(table, row, ledger, external), nil
}

// nicPrefixUnowned changes known network contributions and retains unknown baseline.
func (o *NB) nicPrefixUnowned(ctx context.Context, table string, names []string, add, remove []net.IPNet, ensure bool) (bool, error) {
	rows := []ovsdb.Row{}
	for _, name := range names {
		selected, err := o.nicPrefixRead(ctx, table, "name", name)
		if err != nil {
			return true, err
		}

		if len(selected) == 0 {
			rows = append(rows, nil)
			continue
		}

		if len(selected) != 1 {
			return true, errors.New("Shared prefix row is ambiguous")
		}

		rows = append(rows, selected[0])
	}

	missing := 0
	for _, row := range rows {
		if row == nil {
			missing++
		}
	}

	if missing == len(rows) {
		return false, nil
	}

	if missing > 0 {
		return true, errors.New("Shared prefix row is partially missing")
	}

	// Rows no NIC ever published to carry no ownership; they keep their ordinary update.
	tracked := false
	for _, row := range rows {
		tracked = tracked || nicPrefixLedgerValue(row) != ""
	}

	if !tracked {
		return false, nil
	}

	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID)}
	for i, row := range rows {
		ledger, external, err := o.nicPrefixLedger(table, row, true)
		if err != nil {
			return true, err
		}

		family := 0
		if table == "Address_Set" {
			family = []int{4, 6}[i]
		}

		// The network's own removals also withdraw unowned baseline entries, as they always did;
		// only contributions of NIC owners are kept.
		for _, prefix := range nicPrefixRequested(remove, family) {
			ledger.Managed = slices.DeleteFunc(ledger.Managed, func(value string) bool { return value == prefix })
			ledger.Baseline = slices.DeleteFunc(ledger.Baseline, func(value string) bool { return value == prefix })
		}

		current := ledger.addresses()
		for _, prefix := range nicPrefixRequested(add, family) {
			if !ensure || !slices.Contains(current, prefix) {
				ledger.Managed = append(ledger.Managed, prefix)
			}
		}

		ledger.Managed = nicPrefixCanonical(ledger.Managed)
		operations = append(operations, nicPrefixRowWait(table, row), nicPrefixUpdate(table, row, ledger, external))
	}

	return true, o.nicPrefixCommit(ctx, operations)
}

// EnsureAddressSetPrefixes repairs visibility without adopting known NIC prefixes.
func (o *NB) EnsureAddressSetPrefixes(ctx context.Context, prefix OVNAddressSet, addresses ...net.IPNet) error {
	return o.updateAddressSets(ctx, prefix, addresses, nil, true, true)
}

func nicPrefixVersionGuard(row ovsdb.Row, capturedVersion string, capturedLedger string) error {
	// Row versions are server-local and regenerated when a database server restarts. The ledger read
	// requires the field to equal its recorded owners, and every write waits on the full row content,
	// so a stored plan does not depend on the captured version.
	_, err := nicCleanupRowUUID(row, "_version")
	return err
}

// ClearNICARPProxyPrefixes withdraws every proxy entry, keeping receipts for NIC contributions.
func (o *NB) ClearNICARPProxyPrefixes(ctx context.Context, port OVNSwitchPort) error {
	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "name", string(port))
	if err != nil || len(rows) != 1 {
		return errors.Join(err, errors.New("Shared proxy row is absent or ambiguous"))
	}

	row := rows[0]
	options, err := nicCleanupStringMap(row["options"])
	if err != nil || row["type"] != "router" || options["router-port"] == "" {
		return errors.New("Shared proxy port is repurposed")
	}

	id, err := nicCleanupRowUUID(row, "_uuid")
	if err != nil {
		return err
	}

	parents, err := o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch", Where: []ovsdb.Condition{{Column: "ports", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: id}}}}}, Columns: []string{"_uuid", "name"}})
	if err != nil || len(parents[1].Rows) != 1 {
		return errors.Join(err, errors.New("Shared proxy parent is absent or ambiguous"))
	}

	parentID, err := nicCleanupRowUUID(parents[1].Rows[0], "_uuid")
	if err != nil {
		return err
	}

	name, ok := parents[1].Rows[0]["name"].(string)
	if !ok || name == "" {
		return errors.New("Shared proxy parent name is invalid")
	}

	identity := NICPortCleanup{SwitchUUID: parentID, SwitchName: OVNSwitch(name), PortUUID: id, PortName: port}
	guards := []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity), nicPrefixRowWait("Logical_Switch_Port", row)}
	if nicPrefixLedgerValue(row) == "" {
		// No NIC ever published here: clear the whole option as before.
		if options["arp_proxy"] == "" {
			return o.nicPrefixCommit(ctx, guards)
		}

		delete(options, "arp_proxy")
		return o.nicPrefixCommit(ctx, append(guards, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Logical_Switch_Port", Where: nicPrefixRowWait("Logical_Switch_Port", row).Where, Row: ovsdb.Row{"options": nicCleanupStringMapWire(options)}}))
	}

	ledger, external, err := o.nicPrefixLedger("Logical_Switch_Port", row, false)
	if err != nil {
		return err
	}

	// Every proxy entry is withdrawn; NIC contributions keep receipts for their later cleanup.
	for generation, contribution := range ledger.Owners {
		ledger.Released[generation] = contribution
		delete(ledger.Owners, generation)
	}

	ledger.Baseline, ledger.Managed = []string{}, []string{}
	return o.nicPrefixCommit(ctx, append(guards, nicPrefixUpdate("Logical_Switch_Port", row, ledger, external)))
}

func nicPrefixLedgerValue(row ovsdb.Row) string {
	ids, _ := nicCleanupStringMap(row["external_ids"])
	return ids[nicPrefixMetadata]
}
