package ovn

import (
	"context"
	"errors"
	"net"
	"slices"

	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NICARPProxyCleanup binds shared-proxy retirement to the actual NIC allocation.
type NICARPProxyCleanup struct {
	Version     int
	RootUUID    string
	SwitchUUID  string
	SwitchName  OVNSwitch
	PortUUID    string
	PortVersion string
	Ledger      string
	RouterPort  string
	PortName    OVNSwitchPort
	Before      map[string]string
	After       map[string]string
	Ownership   NICPrefixCleanup
}

func nicCleanupStringMapWire(values map[string]string) ovsdb.OvsMap {
	wire := ovsdb.OvsMap{GoMap: make(map[any]any, len(values))}
	for key, value := range values {
		wire.GoMap[key] = value
	}

	return wire
}

// Validate checks the original shared proxy capture.
func (plan NICARPProxyCleanup) Validate(root string, port OVNSwitchPort, addresses []net.IPNet) error {
	if plan.Version != 2 || plan.RootUUID != root || !nicCleanupUUID(plan.SwitchUUID) || !nicCleanupUUID(plan.PortUUID) || !nicCleanupUUID(plan.PortVersion) || len(plan.Ledger) != 64 || plan.RouterPort == "" || plan.SwitchName == "" || port == "" || plan.PortName != port {
		return errors.New("Original NIC shared proxy identity or ownership proof is missing")
	}

	if !slices.Equal(plan.Ownership.Prefixes, nicPrefixRequested(addresses, 0)) {
		return errors.New("Original NIC proxy allocation differs from source prefixes")
	}

	return plan.Ownership.validate(root, addresses)
}

// CapturedPrefixes derives the exact published proxy contribution.
func (plan NICARPProxyCleanup) CapturedPrefixes() ([]net.IPNet, error) {
	return nicPrefixCaptured(plan.Ownership.Prefixes)
}

func (plan NICARPProxyCleanup) portIdentity() NICPortCleanup {
	return NICPortCleanup{SwitchUUID: plan.SwitchUUID, SwitchName: plan.SwitchName, PortUUID: plan.PortUUID, PortName: plan.PortName}
}

// CaptureNICARPProxyCleanup captures the exact original shared proxy contribution.
func (o *NB) CaptureNICARPProxyCleanup(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, addresses []net.IPNet, owners ...NICPrefixOwner) (NICARPProxyCleanup, error) {
	if len(owners) != 1 {
		return NICARPProxyCleanup{}, errors.New("NIC proxy provenance is unknown; preserving shared contributions")
	}

	identity, err := o.nicPrefixProxyIdentity(ctx, sw, port)
	if err != nil {
		return NICARPProxyCleanup{}, err
	}

	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: identity.PortUUID})
	if err != nil || len(rows) != 1 {
		return NICARPProxyCleanup{}, errors.Join(err, errors.New("Original shared proxy port is unavailable"))
	}

	row := rows[0]
	version, err := nicCleanupRowUUID(row, "_version")
	if err != nil {
		return NICARPProxyCleanup{}, err
	}

	proof, err := o.nicPrefixCapture("Logical_Switch_Port", row, addresses, owners[0])
	if err != nil {
		return NICARPProxyCleanup{}, err
	}

	options, err := nicCleanupStringMap(row["options"])
	if err != nil {
		return NICARPProxyCleanup{}, err
	}

	plan := NICARPProxyCleanup{Version: 2, RootUUID: o.backendID, SwitchUUID: identity.SwitchUUID, SwitchName: sw, PortUUID: identity.PortUUID, PortVersion: version, PortName: port, Ownership: proof, RouterPort: options["router-port"], Ledger: nicPrefixDigest(nicPrefixLedgerValue(row))}
	captured, err := plan.CapturedPrefixes()
	if err != nil {
		return plan, err
	}

	err = plan.Validate(o.backendID, port, captured)
	if err != nil {
		return plan, err
	}

	err = o.nicPrefixCommit(ctx, []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(identity), nicCleanupPortOwnerWait(identity), nicPrefixRowWait("Logical_Switch_Port", row)})
	return plan, err
}

// ApplyNICARPProxyCleanup retires the captured original shared proxy contribution.
func (o *NB) ApplyNICARPProxyCleanup(ctx context.Context, plan NICARPProxyCleanup, port OVNSwitchPort, addresses []net.IPNet) error {
	err := plan.Validate(o.backendID, port, addresses)
	if err != nil {
		return err
	}

	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: plan.PortUUID})
	if err != nil || len(rows) != 1 {
		return errors.Join(err, errors.New("Original shared proxy row is unavailable; retaining debt"))
	}

	row := rows[0]
	options, err := nicCleanupStringMap(row["options"])
	if err != nil || row["name"] != string(plan.PortName) || row["type"] != "router" || options["router-port"] != plan.RouterPort {
		return errors.New("Original shared proxy port was renamed or repurposed")
	}

	err = nicPrefixVersionGuard(row, plan.PortVersion, plan.Ledger)
	if err != nil {
		return err
	}

	effect, err := o.nicPrefixRelease("Logical_Switch_Port", row, plan.Ownership)
	if err != nil {
		return err
	}

	return o.nicPrefixCommit(ctx, []ovsdb.Operation{nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan.portIdentity()), nicCleanupPortOwnerWait(plan.portIdentity()), nicPrefixRowWait("Logical_Switch_Port", row), effect})
}

// RollbackNICARPProxyPrefixes restores a captured source contribution.
func (o *NB) RollbackNICARPProxyPrefixes(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, addresses []net.IPNet, owner NICPrefixOwner) error {
	plan, err := o.CaptureNICARPProxyCleanup(ctx, sw, port, addresses, owner)
	if err != nil {
		return err
	}

	return o.ApplyNICARPProxyCleanup(ctx, plan, port, addresses)
}
