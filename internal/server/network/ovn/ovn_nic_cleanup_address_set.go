package ovn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICAddressSetCleanup binds shared-row retirement to the actual NIC allocation.
type NICAddressSetCleanup struct {
	Version  int
	RootUUID string
	Rows     []NICAddressSetCleanupRow
}

// NICAddressSetCleanupRow records an exact original address-set row.
type NICAddressSetCleanupRow struct {
	UUID      string
	Version   string
	Ledger    string
	Name      string
	Before    []string
	After     []string
	Ownership NICPrefixCleanup
}

func nicCleanupStringSet(value any) ([]string, error) {
	var values []any
	switch value := value.(type) {
	case string:
		values = []any{value}
	case ovsdb.OvsSet:
		values = value.GoSet
	default:
		return nil, errors.New("Invalid NIC cleanup string set")
	}

	result := make([]string, 0, len(values))
	for _, value := range values {
		entry, ok := value.(string)
		if !ok || slices.Contains(result, entry) {
			return nil, errors.New("Invalid or duplicate NIC cleanup string set entry")
		}

		result = append(result, entry)
	}

	slices.Sort(result)
	return result, nil
}

func nicCleanupStringSetWire(values []string) ovsdb.OvsSet {
	wire := ovsdb.OvsSet{GoSet: make([]any, 0, len(values))}
	for _, value := range values {
		wire.GoSet = append(wire.GoSet, value)
	}

	return wire
}

// Validate checks the original address-set capture.
func (plan NICAddressSetCleanup) Validate(root string, prefix OVNAddressSet, addresses []net.IPNet) error {
	if plan.Version != 2 || plan.RootUUID != root || !nicCleanupUUID(root) || prefix == "" || len(plan.Rows) != 2 {
		return errors.New("Original NIC address-set ownership proof is missing or unsupported")
	}

	for i, row := range plan.Rows {
		if !nicCleanupUUID(row.UUID) || !nicCleanupUUID(row.Version) || len(row.Ledger) != 64 || row.Name != fmt.Sprintf("%s_ip%d", prefix, []int{4, 6}[i]) || (i > 0 && row.UUID == plan.Rows[0].UUID) {
			return errors.New("Original NIC address-set identity is invalid")
		}

		err := row.Ownership.validate(root, addresses)
		if err != nil {
			return err
		}

		if !slices.Equal(row.Ownership.Prefixes, nicPrefixRequested(addresses, []int{4, 6}[i])) {
			return errors.New("Original NIC address-set allocation differs from source family")
		}

		if i > 0 && row.Ownership.Owner != plan.Rows[0].Ownership.Owner {
			return errors.New("Original NIC address-set owners disagree")
		}
	}

	return nil
}

// CapturedPrefixes derives the exact published contribution, excluding candidate-only routes.
func (plan NICAddressSetCleanup) CapturedPrefixes() ([]net.IPNet, error) {
	var values []string
	for _, row := range plan.Rows {
		values = append(values, row.Ownership.Prefixes...)
	}

	return nicPrefixCaptured(values)
}

func nicPrefixCaptured(values []string) ([]net.IPNet, error) {
	var addresses []net.IPNet
	for _, value := range values {
		_, prefix, err := net.ParseCIDR(value)
		if err != nil || prefix.String() != value {
			return nil, errors.New("Original NIC shared prefix is invalid")
		}

		addresses = append(addresses, *prefix)
	}

	return addresses, nil
}

// CaptureNICAddressSetCleanup uses ownership published with the actual effect.
func (o *NB) CaptureNICAddressSetCleanup(ctx context.Context, prefix OVNAddressSet, addresses []net.IPNet, owners ...NICPrefixOwner) (NICAddressSetCleanup, error) {
	if len(owners) != 1 {
		return NICAddressSetCleanup{}, errors.New("NIC address-set provenance is unknown; preserving shared contributions")
	}

	plan := NICAddressSetCleanup{Version: 2, RootUUID: o.backendID}
	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID)}
	for _, family := range []int{4, 6} {
		name := fmt.Sprintf("%s_ip%d", prefix, family)
		selected, err := o.nicPrefixRead(ctx, "Address_Set", "name", name)
		if err != nil || len(selected) != 1 {
			return plan, errors.Join(err, errors.New("Original NIC address set is absent or ambiguous"))
		}

		row := selected[0]
		id, err := nicCleanupRowUUID(row, "_uuid")
		if err != nil {
			return plan, err
		}

		version, err := nicCleanupRowUUID(row, "_version")
		if err != nil {
			return plan, err
		}

		proof, err := o.nicPrefixCapture("Address_Set", row, addresses, owners[0])
		if err != nil {
			return plan, err
		}

		plan.Rows = append(plan.Rows, NICAddressSetCleanupRow{UUID: id, Version: version, Name: name, Ownership: proof, Ledger: nicPrefixDigest(nicPrefixLedgerValue(row))})
		operations = append(operations, nicPrefixRowWait("Address_Set", row))
	}

	captured, err := plan.CapturedPrefixes()
	if err != nil {
		return plan, err
	}

	err = plan.Validate(o.backendID, prefix, captured)
	if err != nil {
		return plan, err
	}

	err = o.nicPrefixCommit(ctx, operations)
	return plan, err
}

// ApplyNICAddressSetCleanup retires only the original owner and records its receipt.
func (o *NB) ApplyNICAddressSetCleanup(ctx context.Context, plan NICAddressSetCleanup, prefix OVNAddressSet, addresses []net.IPNet) error {
	err := plan.Validate(o.backendID, prefix, addresses)
	if err != nil {
		return err
	}

	operations := []ovsdb.Operation{nicCleanupRootWait(o.backendID)}
	for _, captured := range plan.Rows {
		rows, err := o.nicPrefixRead(ctx, "Address_Set", "_uuid", ovsdb.UUID{GoUUID: captured.UUID})
		if err != nil || len(rows) != 1 {
			return errors.Join(err, errors.New("Original shared address-set row is unavailable; retaining debt"))
		}

		row := rows[0]
		if row["name"] != captured.Name {
			return errors.New("Original NIC address-set row was renamed")
		}

		err = nicPrefixVersionGuard(row, captured.Version, captured.Ledger)
		if err != nil {
			return err
		}

		effect, err := o.nicPrefixRelease("Address_Set", row, captured.Ownership)
		if err != nil {
			return err
		}

		operations = append(operations, nicPrefixRowWait("Address_Set", row), effect)
	}

	return o.nicPrefixCommit(ctx, operations)
}

// RollbackNICAddressSetPrefixes also handles a lost committed Start reply.
func (o *NB) RollbackNICAddressSetPrefixes(ctx context.Context, prefix OVNAddressSet, addresses []net.IPNet, owner NICPrefixOwner) error {
	plan, err := o.CaptureNICAddressSetCleanup(ctx, prefix, addresses, owner)
	if err != nil {
		return err
	}

	return o.ApplyNICAddressSetCleanup(ctx, plan, prefix, addresses)
}
