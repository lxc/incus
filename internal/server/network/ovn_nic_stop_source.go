package network

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
)

// ovnNICStopSourceSnapshot preserves original source inputs and guarded row
// identities independently of current instance placement/Desired configuration.
// Publication alone is not acknowledgment of all backend, BGP or host effects.
type ovnNICStopSourceSnapshot struct {
	Kind         string
	Version      int
	NetworkID    int64
	InstanceID   int
	DeviceConfig deviceConfig.Device
	HostVolatile map[string]string
	Port         networkOVN.NICPortCleanup
	OVS          *ovs.NICPortCleanup
	DNSIPs       []net.IP
	Prefixes     []net.IPNet
	ARPPrefixes  []net.IPNet
	ProxyPort    networkOVN.OVNSwitchPort
	RouterPort   networkOVN.OVNRouterPort
	Uplink       bool
	DNSConfig    map[string]string
	MACBindings  *networkOVN.NICMACBindingCleanup
	NAT          *networkOVN.NICNATCleanup
	ARPProxy     *networkOVN.NICARPProxyCleanup
	AddressSets  *networkOVN.NICAddressSetCleanup
	Routes       []networkOVN.NICRouteCleanup

	// PrefixOwnerTransferred records a previous member's prefix owner on a cold-moved port whose
	// producer was transferred here; its contributions were released before the transfer.
	PrefixOwnerTransferred bool
}

// prefixOwnerPort is the port identity prefix owners are bound to; a transferred owner keeps the
// exact root/switch/port identity but names its previous member.
func (s ovnNICStopSourceSnapshot) prefixOwnerPort() networkOVN.NICPortCleanup {
	port := s.Port
	if s.PrefixOwnerTransferred {
		port.Source = ""
	}

	return port
}

func (s ovnNICStopSourceSnapshot) validate(a db.OVNNICCleanup) error {
	if s.Kind != "incus-ovn-nic-stop" || s.Version != 1 || s.InstanceID <= 0 || !slices.Contains(a.NetworkIDs, s.NetworkID) || s.Port.Version != 1 || s.Port.Source == "" {
		return errors.New("Original NIC cleanup source snapshot is unsupported or incomplete")
	}

	for _, value := range []string{s.Port.RootUUID, s.Port.SwitchUUID, s.Port.PortUUID, s.Port.PortVersion} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("Original NIC cleanup port row identity is invalid")
		}
	}

	if s.Port.SwitchName == "" || s.Port.PortName == "" {
		return errors.New("Original NIC cleanup port parent/name is missing")
	}

	if s.DeviceConfig["nested"] == "" {
		if s.OVS == nil {
			return errors.New("Original NIC OVS cleanup capture is missing")
		}

		err := s.OVS.Validate()
		if err != nil {
			return err
		}

		if s.OVS.OVNPortName != string(s.Port.PortName) {
			return errors.New("Original NIC OVS/NB port identities disagree")
		}
	} else if s.OVS != nil {
		return errors.New("Nested NIC cannot adopt a host OVS cleanup capture")
	}

	tuples, err := nicStopNATTuples(s.DeviceConfig, s.DNSIPs)
	if err != nil {
		return err
	}

	if len(tuples) > 0 && s.NAT == nil {
		return errors.New("Original NIC NAT capture is missing")
	}

	if s.NAT != nil {
		if s.NAT.Version != 1 || s.NAT.RootUUID != s.Port.RootUUID || s.NAT.RouterName == "" {
			return errors.New("Original NIC NAT source identity is invalid")
		}

		id, err := uuid.Parse(s.NAT.RouterUUID)
		if err != nil || id == uuid.Nil || id.String() != s.NAT.RouterUUID {
			return errors.New("Original NIC NAT router UUID is invalid")
		}

		for _, row := range s.NAT.Rows {
			tuple := networkOVN.NICNATCleanupTuple{Type: row.Type, LogicalIP: row.LogicalIP, ExternalIP: row.ExternalIP}
			if !slices.Contains(tuples, tuple) {
				return errors.New("Original NIC NAT translation disagrees with source inputs")
			}

			for _, value := range []string{row.UUID, row.Version} {
				id, err := uuid.Parse(value)
				if err != nil || id == uuid.Nil || id.String() != value {
					return errors.New("Original NIC NAT row UUID is invalid")
				}
			}
		}
	}

	if len(s.DNSIPs) > 0 && s.MACBindings == nil {
		return errors.New("Original NIC MAC binding capture is missing")
	}

	if s.MACBindings != nil {
		err := s.MACBindings.Validate()
		if err != nil {
			return err
		}

		if s.MACBindings.NBRootUUID != s.Port.RootUUID || s.MACBindings.Port != s.RouterPort {
			return errors.New("Original NIC MAC binding source roots/port disagree")
		}

		routerBound := s.NAT != nil && s.NAT.RouterUUID == s.MACBindings.NBRouterUUID
		for _, route := range s.Routes {
			routerBound = routerBound || route.RouterUUID == s.MACBindings.NBRouterUUID
		}

		if !routerBound {
			return errors.New("Original NIC MAC binding NB router identity is missing")
		}

		for _, row := range s.MACBindings.Rows {
			found := false
			for _, ip := range s.DNSIPs {
				found = found || ip.String() == row.IP
			}

			if !found {
				return errors.New("Original NIC MAC binding IP disagrees with source inputs")
			}
		}
	}

	if len(s.Prefixes) > 0 {
		if s.AddressSets == nil {
			return errors.New("Original NIC address-set capture is missing")
		}

		capturedPrefixes, err := s.AddressSets.CapturedPrefixes()
		if err != nil {
			return err
		}

		for _, prefix := range capturedPrefixes {
			if !slices.ContainsFunc(s.Prefixes, func(candidate net.IPNet) bool { return candidate.String() == prefix.String() }) {
				return errors.New("Original address-set contribution is outside stopped source candidates")
			}
		}

		err = s.AddressSets.Validate(s.Port.RootUUID, acl.OVNIntSwitchPortGroupAddressSetPrefix(s.NetworkID), capturedPrefixes)
		if err == nil {
			err = s.AddressSets.Rows[0].Ownership.Owner.Validate(s.Port.RootUUID, s.prefixOwnerPort())
		}

		if err != nil {
			return err
		}
	} else if s.AddressSets != nil {
		return errors.New("Original NIC address-set capture has no source prefixes")
	}

	if s.Uplink && len(s.ARPPrefixes) > 0 {
		if s.ARPProxy == nil {
			return errors.New("Original NIC ARP proxy capture is missing")
		}

		capturedPrefixes, err := s.ARPProxy.CapturedPrefixes()
		if err != nil {
			return err
		}

		for _, prefix := range capturedPrefixes {
			if !slices.ContainsFunc(s.ARPPrefixes, func(candidate net.IPNet) bool { return candidate.String() == prefix.String() }) {
				return errors.New("Original proxy contribution is outside stopped source candidates")
			}
		}

		err = s.ARPProxy.Validate(s.Port.RootUUID, s.ProxyPort, capturedPrefixes)
		if err == nil {
			err = s.ARPProxy.Ownership.Owner.Validate(s.Port.RootUUID, s.prefixOwnerPort())
		}

		if err == nil && s.AddressSets != nil && s.ARPProxy.Ownership.Owner != s.AddressSets.Rows[0].Ownership.Owner {
			err = errors.New("Original shared-prefix allocation owners disagree")
		}

		if err != nil {
			return err
		}
	} else if s.ARPProxy != nil {
		return errors.New("Original NIC ARP proxy capture has no applicable source prefixes")
	}

	for _, route := range s.Routes {
		if route.RootUUID != s.Port.RootUUID {
			return errors.New("Original NIC cleanup source roots disagree")
		}
	}

	for _, ip := range s.DNSIPs {
		if ip.To16() == nil {
			return errors.New("Invalid original NIC cleanup DNS IP")
		}
	}

	for _, prefix := range slices.Concat(s.Prefixes, s.ARPPrefixes) {
		ones, bits := prefix.Mask.Size()
		if prefix.IP.To16() == nil || bits == 0 || ones < 0 {
			return errors.New("Invalid original NIC cleanup prefix")
		}
	}

	return nil
}

func (n *ovn) pendingNICStopSource(ctx context.Context, instanceUUID string, deviceName string) (*ovnNICStopSourceSnapshot, *db.OVNNICCleanup, error) {
	var attempt db.OVNNICCleanup
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		attempt, err = tx.OVNNICCleanupForDevice(ctx, instanceUUID, deviceName)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	if attempt.Version != 1 {
		return nil, nil, errors.New("Original NIC cleanup record version is unsupported")
	}

	var source ovnNICStopSourceSnapshot
	err = json.Unmarshal([]byte(attempt.Payload), &source)
	if err != nil {
		return nil, nil, fmt.Errorf("Invalid original NIC cleanup source snapshot: %w", err)
	}

	err = source.validate(attempt)
	if err != nil {
		return nil, nil, err
	}

	if source.NetworkID != n.ID() || attempt.SourceNodeID != n.state.DB.Cluster.GetNodeID() || source.Port.Source != n.state.ServerName {
		return nil, nil, errors.New("Original NIC cleanup network/source member changed; refusing current Desired replacement")
	}

	return &source, &attempt, nil
}

// InstanceDevicePortStopSource returns a detached view of the durable original
// source after capture. Missing debt is an error, not proof of completed cleanup.
// This read does not acknowledge effects or release a reservation.
func (n *ovn) InstanceDevicePortStopSource(ctx context.Context, instanceUUID string, deviceName string) (*OVNInstanceNICStopOpts, error) {
	if ctx == nil {
		return nil, errors.New("NIC cleanup source read requires a context")
	}

	source, attempt, err := n.pendingNICStopSource(ctx, instanceUUID, deviceName)
	if err != nil {
		return nil, err
	}

	if source == nil || attempt == nil {
		return nil, fmt.Errorf("Original NIC cleanup source is missing: %w", sql.ErrNoRows)
	}

	return &OVNInstanceNICStopOpts{
		CleanupGeneration: attempt.Generation, NetworkID: source.NetworkID,
		InstanceUUID: attempt.InstanceUUID, InstanceID: source.InstanceID,
		DeviceName: attempt.DeviceName, DeviceConfig: source.DeviceConfig.Clone(),
		HostVolatile: maps.Clone(source.HostVolatile), OVS: cloneNICStopOVS(source.OVS),
	}, nil
}

// nicStopCheckGeneration prevents a later stage from silently adopting a newer
// pending attempt after it has already selected the original device inputs.
func nicStopCheckGeneration(expected string, attempt *db.OVNNICCleanup) error {
	if expected != "" && (attempt == nil || attempt.Generation != expected) {
		return errors.New("Original NIC cleanup generation changed or is missing")
	}

	return nil
}

func (n *ovn) publishNICStopSource(ctx context.Context, opts *OVNInstanceNICStopOpts, source ovnNICStopSourceSnapshot, tokens map[int64]string) (db.OVNNICCleanup, error) {
	if opts == nil || source.Port.Source != n.state.ServerName {
		return db.OVNNICCleanup{}, errors.New("Original NIC cleanup source member identity is missing or changed")
	}

	source.Kind = "incus-ovn-nic-stop"
	source.Version = 1
	source.NetworkID = n.ID()
	source.InstanceID = opts.InstanceID
	source.DeviceConfig = opts.DeviceConfig.Clone()
	source.HostVolatile = maps.Clone(opts.HostVolatile)
	source.OVS = cloneNICStopOVS(opts.OVS)
	ids := make([]int64, 0, len(tokens))
	for id := range tokens {
		ids = append(ids, id)
	}

	slices.Sort(ids)
	a := db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: n.state.DB.Cluster.GetNodeID(), InstanceUUID: opts.InstanceUUID, DeviceName: opts.DeviceName, Version: 1, NetworkIDs: ids}
	err := source.validate(a)
	if err != nil {
		return db.OVNNICCleanup{}, err
	}

	payload, err := json.Marshal(source)
	if err != nil {
		return db.OVNNICCleanup{}, err
	}

	a.Payload = string(payload)
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CaptureOVNNICCleanup(ctx, a, tokens)
	})
	if err != nil {
		return db.OVNNICCleanup{}, err
	}

	return a, nil
}

func (n *ovn) reserveOriginalNICStopSet(ctx context.Context, a db.OVNNICCleanup) (func() error, error) {
	inherited, err := n.nicStopInheritedRouteTokens(a.NetworkIDs)
	if err != nil {
		return nil, err
	}

	var reservations []db.OVNNICCleanupReservation
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		reservations, err = tx.AcquireOVNNICCleanupOperations(ctx, a.NetworkIDs, inherited)
		return err
	})
	if err != nil {
		return nil, err
	}

	return func() error {
		return n.state.DB.Cluster.Transaction(context.WithoutCancel(ctx), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNICCleanupOperations(ctx, reservations)
		})
	}, nil
}

func nicStopNATTuples(config deviceConfig.Device, ips []net.IP) ([]networkOVN.NICNATCleanupTuple, error) {
	var tuples []networkOVN.NICNATCleanupTuple
	for _, family := range []string{"ipv4", "ipv6"} {
		value := config[family+".address.external"]
		if value == "" {
			continue
		}

		external := net.ParseIP(value)
		want4 := family == "ipv4"
		if external == nil || (external.To4() != nil) != want4 {
			return nil, fmt.Errorf("Invalid original NIC %s external address", family)
		}

		var internal net.IP
		for _, ip := range ips {
			if ip.To16() == nil || (ip.To4() != nil) != want4 {
				continue
			}

			if internal != nil && !internal.Equal(ip) {
				return nil, fmt.Errorf("Ambiguous original NIC %s internal address", family)
			}

			internal = ip
		}

		if internal == nil {
			return nil, fmt.Errorf("Original NIC %s NAT internal address is unavailable", family)
		}

		prefix := IPToNet(internal)
		tuples = append(tuples,
			networkOVN.NICNATCleanupTuple{Type: "snat", LogicalIP: prefix.String(), ExternalIP: external.String()},
			networkOVN.NICNATCleanupTuple{Type: "dnat_and_snat", LogicalIP: internal.String(), ExternalIP: external.String()})
	}

	return tuples, nil
}

// nicStopMACRouterUUID links MAC invalidation to the actually captured local
// router row; a name alone or a route plan for only a peer is insufficient.
func nicStopMACRouterUUID(nbRoot string, routerName networkOVN.OVNRouter, plans []networkOVN.NICRouteCleanup) (string, error) {
	var routerUUID string
	for _, plan := range plans {
		if plan.RouterName != routerName {
			continue
		}

		id, err := uuid.Parse(plan.RouterUUID)
		if err != nil || id == uuid.Nil || id.String() != plan.RouterUUID || plan.RootUUID != nbRoot || routerUUID != "" {
			return "", errors.New("Original NIC local router capture is invalid or ambiguous")
		}

		routerUUID = plan.RouterUUID
	}

	if routerUUID == "" {
		return "", errors.New("Original NIC local router capture is missing")
	}

	return routerUUID, nil
}

// OVNNICCleanupSource decodes a database-validated pending record for its
// original source member. It never consults the instance's current Desired
// devices or placement. Loaded network identity must be checked before use.
func OVNNICCleanupSource(a db.OVNNICCleanup, sourceNodeID int64, sourceName string) (*OVNInstanceNICStopOpts, int64, error) {
	if a.Completed || a.Version != 1 || a.SourceNodeID != sourceNodeID || sourceName == "" || a.DeviceName == "" {
		return nil, 0, errors.New("Original NIC cleanup record is not pending for this source member")
	}

	for _, value := range []string{a.Generation, a.InstanceUUID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return nil, 0, errors.New("Original NIC cleanup attempt identity is invalid")
		}
	}

	var source ovnNICStopSourceSnapshot
	err := json.Unmarshal([]byte(a.Payload), &source)
	if err != nil {
		return nil, 0, fmt.Errorf("Invalid original NIC cleanup source snapshot: %w", err)
	}

	err = source.validate(a)
	if err != nil {
		return nil, 0, err
	}

	if source.Port.Source != sourceName || source.DeviceConfig["type"] != "nic" || source.DeviceConfig["network"] == "" {
		return nil, 0, errors.New("Original NIC cleanup device/source identity is missing or changed")
	}

	return &OVNInstanceNICStopOpts{
		CleanupGeneration: a.Generation, NetworkID: source.NetworkID, InstanceUUID: a.InstanceUUID, InstanceID: source.InstanceID,
		DeviceName: a.DeviceName, DeviceConfig: source.DeviceConfig.Clone(), HostVolatile: maps.Clone(source.HostVolatile), OVS: cloneNICStopOVS(source.OVS),
	}, source.NetworkID, nil
}

// InstanceDevicePortStopRetire preserves source identity under the entire
// original reservation set while retiring metadata only. Full cleanup debt stays
// pending. Caller must have completed required effects before entering here.
func (n *ovn) InstanceDevicePortStopRetire(ctx context.Context, instanceUUID string, deviceName string, generation string) (bool, error) {
	if ctx == nil || generation == "" {
		return false, errors.New("NIC volatile retirement requires context and original generation")
	}

	_, a, err := n.pendingNICStopSource(ctx, instanceUUID, deviceName)
	if err != nil {
		return false, err
	}

	err = nicStopCheckGeneration(generation, a)
	if err != nil {
		return false, err
	}

	inherited, err := n.nicStopInheritedRouteTokens(a.NetworkIDs)
	if err != nil {
		return false, err
	}

	var cleared bool
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		reservations, err := tx.AcquireOVNNICCleanupOperations(ctx, a.NetworkIDs, inherited)
		if err != nil {
			return err
		}

		tokens := make(map[int64]string, len(reservations))
		for _, reservation := range reservations {
			tokens[reservation.NetworkID] = reservation.Token
		}

		cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, *a, tokens)
		if err != nil {
			return err
		}
		// This phase is SQL only; successful release is in the same transaction.
		return tx.ReleaseOVNNICCleanupOperations(ctx, reservations)
	})
	return cleared && err == nil, err
}

// cloneNICStopOVS keeps detached source views from changing the durable plan.
func cloneNICStopOVS(plan *ovs.NICPortCleanup) *ovs.NICPortCleanup {
	if plan == nil {
		return nil
	}

	cloned := *plan
	return &cloned
}

// InstanceDevicePortStopComplete records terminal acknowledgment after the host
// hook, BGP withdrawal, rooted backend cleanup and volatile retirement succeed.
func (n *ovn) InstanceDevicePortStopComplete(ctx context.Context, instanceUUID string, deviceName string, generation string) (err error) {
	if ctx == nil || generation == "" {
		return errors.New("NIC terminal acknowledgment requires context and original generation")
	}

	var a db.OVNNICCleanup
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		a, err = tx.OVNNICCleanupByGeneration(ctx, generation)
		return err
	})
	if err != nil {
		return err
	}

	if a.InstanceUUID != instanceUUID || a.DeviceName != deviceName || a.SourceNodeID != n.state.DB.Cluster.GetNodeID() || !slices.Contains(a.NetworkIDs, n.ID()) {
		return errors.New("Original NIC terminal acknowledgment identity changed")
	}

	var source ovnNICStopSourceSnapshot
	err = json.Unmarshal([]byte(a.Payload), &source)
	if err == nil {
		err = source.validate(a)
	}

	if err != nil {
		return err
	}

	if source.NetworkID != n.ID() || source.Port.Source != n.state.ServerName {
		return errors.New("Original NIC terminal source member/network changed")
	}

	if a.Completed {
		return nil
	}

	// Instance-level terminal and live-detach acknowledgments run after the device fence was released.
	release, err := n.waitLocalLifecycle()
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	inherited, err := n.nicStopInheritedRouteTokens(a.NetworkIDs)
	if err != nil {
		return err
	}

	err = n.state.DB.Cluster.Transaction(context.WithoutCancel(ctx), func(ctx context.Context, tx *db.ClusterTx) error {
		reservations, err := tx.AcquireOVNNICCleanupOperations(ctx, a.NetworkIDs, inherited)
		if err != nil {
			return err
		}

		tokens := make(map[int64]string, len(reservations))
		for _, reservation := range reservations {
			tokens[reservation.NetworkID] = reservation.Token
		}
		// Only the successful NIC hook may create this original effects receipt.
		err = tx.EnsureOVNNICCleanupRetired(ctx, a)
		if err != nil {
			return err
		}

		err = tx.CompleteOVNNICCleanup(ctx, a, tokens)
		if err != nil {
			return err
		}

		return tx.ReleaseOVNNICCleanupOperations(ctx, reservations)
	})
	if err == nil {
		return nil
	}
	// A failed transaction reply may follow a committed terminal receipt.
	var recorded db.OVNNICCleanup
	readErr := n.state.DB.Cluster.Transaction(context.WithoutCancel(ctx), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		recorded, err = tx.OVNNICCleanupByGeneration(ctx, generation)
		return err
	})
	if readErr == nil && recorded.Completed && recorded.Generation == a.Generation && recorded.SourceNodeID == a.SourceNodeID && recorded.InstanceUUID == a.InstanceUUID && recorded.DeviceName == a.DeviceName && recorded.Version == a.Version && slices.Equal(recorded.NetworkIDs, a.NetworkIDs) && recorded.Payload == a.Payload {
		return nil
	}

	return errors.Join(err, readErr)
}

func (n *ovn) instanceDevicePortMigrationStart(opts *OVNInstanceNICSetupOpts) (networkOVN.OVNSwitchPort, []net.IP, error) {
	ctx := context.Background()
	var m db.OVNNICMigration
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.EnsureOVNNICMigrationStart(ctx, opts.MigrationOperation, opts.InstanceID, opts.InstanceUUID, opts.DeviceName, n.ID(), opts.NICHostVolatile)
		if err != nil {
			return err
		}

		m, err = tx.OVNNICMigrationDevice(ctx, opts.MigrationOperation, opts.DeviceName)
		return err
	})
	if err != nil {
		return "", nil, err
	}

	var source ovnNICStopSourceSnapshot
	err = json.Unmarshal([]byte(m.Source.Payload), &source)
	if err != nil {
		return "", nil, err
	}

	err = source.validate(m.Source)
	if err != nil {
		return "", nil, err
	}

	original, current := source.DeviceConfig.Clone(), opts.DeviceConfig.Clone()
	delete(original, "host_name")
	delete(current, "host_name")
	if source.NetworkID != n.ID() || !maps.Equal(original, current) || source.Port.PortName != n.getInstanceDevicePortName(opts.InstanceUUID, opts.DeviceName) {
		return "", nil, errors.New("Live migration requires unchanged original OVN guest configuration")
	}

	if source.MACBindings != nil {
		err = n.ovnsb.VerifyNICMigrationMACBindings(ctx, *source.MACBindings)
		if err != nil {
			return "", nil, err
		}
	}

	if m.SharedPlan != "" {
		return "", nil, errors.New("Migration retains a prior shared publication attempt")
	}

	vswitch, err := n.state.OVS()
	if err != nil {
		return "", nil, err
	}

	chassis, err := vswitch.GetChassisID(ctx)
	if err != nil {
		return "", nil, err
	}

	router := ""
	if source.NAT != nil {
		router = source.NAT.RouterUUID
	}

	if router == "" && len(source.Routes) > 0 {
		router = source.Routes[0].RouterUUID
	}

	if router == "" && source.MACBindings != nil {
		router = source.MACBindings.NBRouterUUID
	}

	raw, err := json.Marshal(m.TargetVolatile)
	if err != nil {
		return "", nil, err
	}

	generation := uuid.NewSHA1(uuid.MustParse(m.Operation), raw).String()
	// The target republishes exactly the source's published contributions; the stop request also
	// lists DNS addresses that only some modes ever published.
	var prefixes, proxyPrefixes []net.IPNet
	if source.AddressSets != nil {
		prefixes, err = source.AddressSets.CapturedPrefixes()
		if err != nil {
			return "", nil, err
		}
	}

	proxyPort := networkOVN.OVNSwitchPort("")
	if source.Uplink && source.ARPProxy != nil {
		proxyPort = source.ProxyPort
		proxyPrefixes, err = source.ARPProxy.CapturedPrefixes()
		if err != nil {
			return "", nil, err
		}
	}

	var retain []string
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		retain, err = tx.OVNNICMigrationTargetGenerations(ctx, m.InstanceUUID)
		return err
	})
	if err != nil {
		return "", nil, err
	}

	plan, err := n.ovnnb.CaptureNICMigrationShared(ctx, m.Operation, n.state.ServerName, generation, chassis, source.Port, router, acl.OVNIntSwitchPortGroupAddressSetPrefix(n.ID()), prefixes, n.getExtSwitchName(), proxyPort, proxyPrefixes, retain...)
	if err != nil {
		return "", nil, err
	}
	// Original route and NAT generations must still be the captured source rows.
	versions := map[string]string{}
	if source.NAT != nil {
		for _, row := range source.NAT.Rows {
			versions[row.UUID] = row.Version
		}
	}

	for _, route := range source.Routes {
		for _, row := range route.Routes {
			versions[row.UUID] = row.Version
		}
	}

	for id, version := range versions {
		found := false
		for _, row := range plan.Rows {
			if row.Before["_uuid"] == (ovsdb.UUID{GoUUID: id}) && row.Before["_version"] == (ovsdb.UUID{GoUUID: version}) {
				found = true
			}
		}

		if !found {
			return "", nil, errors.New("Original migration shared generation changed")
		}
	}

	data, err := json.Marshal(plan)
	if err != nil {
		return "", nil, err
	}

	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.SetOVNNICMigrationShared(ctx, m, string(data))
	})
	if err != nil {
		return "", nil, err
	}

	err = n.ovnnb.ApplyNICMigrationShared(ctx, plan)
	if err != nil {
		return "", nil, err
	}

	if source.MACBindings != nil {
		err = n.ovnsb.VerifyNICMigrationMACBindings(ctx, *source.MACBindings)
		if err != nil {
			return "", nil, err
		}
	}

	return source.Port.PortName, slices.Clone(source.DNSIPs), nil
}

// InstanceDevicePortMigrationRollback positively restores the exact predecessor.
func (n *ovn) InstanceDevicePortMigrationRollback(ctx context.Context, operation, deviceName string) error {
	var m db.OVNNICMigration
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		m, err = tx.OVNNICMigrationDevice(ctx, operation, deviceName)
		return err
	})
	if err != nil {
		return err
	}

	if m.TargetNodeID != n.state.DB.Cluster.GetNodeID() || m.Phase != "authorized" {
		return errors.New("Migration shared rollback lacks target authority")
	}

	if m.SharedPlan == "" {
		return nil
	}

	var plan networkOVN.NICMigrationShared
	err = json.Unmarshal([]byte(m.SharedPlan), &plan)
	if err != nil {
		return err
	}

	if plan.Operation != m.Operation || plan.Port.Source == n.state.ServerName || plan.Target != n.state.ServerName {
		return errors.New("Migration rollback publication identity changed")
	}

	err = n.ovnnb.RollbackNICMigrationShared(ctx, plan)
	if err != nil {
		return err
	}

	return n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.RollbackOVNNICMigrationShared(ctx, m) })
}

func (n *ovn) nicMigrationPreserveShared(ctx context.Context, a db.OVNNICCleanup, source ovnNICStopSourceSnapshot) (bool, error) {
	var m *db.OVNNICMigration
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		m, err = tx.OVNNICMigrationForCleanup(ctx, a.Generation)
		return err
	})
	if err != nil || m == nil {
		return false, err
	}

	var plan networkOVN.NICMigrationShared
	err = json.Unmarshal([]byte(m.SharedPlan), &plan)
	if err != nil {
		return false, err
	}

	if !m.Ready || plan.Operation != m.Operation || plan.Port != source.Port || plan.Port.Source != n.state.ServerName || m.SourceNodeID != n.state.DB.Cluster.GetNodeID() {
		return false, errors.New("Transferred NIC shared state binding changed")
	}

	err = n.ovnnb.VerifyNICMigrationTransferred(ctx, plan)
	if err != nil {
		return false, err
	}
	// MAC cache verification is a separate observation, never a cross-database commit.
	if source.MACBindings != nil {
		err = n.ovnsb.VerifyNICMigrationMACBindings(ctx, *source.MACBindings)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

// InstanceDevicePortStopRetryValidate checks transferred roots before original local effects.
func (n *ovn) InstanceDevicePortStopRetryValidate(ctx context.Context, instanceUUID, deviceName, generation string) error {
	source, attempt, err := n.pendingNICStopSource(ctx, instanceUUID, deviceName)
	if err != nil {
		return err
	}

	err = nicStopCheckGeneration(generation, attempt)
	if err != nil {
		return err
	}

	if source == nil || attempt == nil {
		return errors.New("Original NIC retry source is missing")
	}

	transferred, err := n.nicMigrationPreserveShared(ctx, *attempt, *source)
	if err != nil {
		return err
	}

	if !transferred {
		return errors.New("Original NIC retry transfer lineage is missing")
	}

	return nil
}

// InstanceDevicePortStopFence retains source authority across local effects and retirement.
func (n *ovn) InstanceDevicePortStopFence(ctx context.Context, instanceUUID, deviceName, generation string) (func() error, error) {
	release, err := AcquireOVNNICOperation(n, true)
	if err != nil {
		return nil, err
	}

	source, attempt, err := n.pendingNICStopSource(ctx, instanceUUID, deviceName)
	if err == nil {
		err = nicStopCheckGeneration(generation, attempt)
	}

	if err == nil && (source == nil || attempt == nil) {
		err = errors.New("Original NIC cleanup effect source is missing")
	}

	if err == nil {
		var transferred bool
		transferred, err = n.nicMigrationPreserveShared(ctx, *attempt, *source)
		if err == nil && !transferred {
			err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.EnsureOVNNICOriginalInstance(ctx, source.InstanceID, instanceUUID)
			})
			if err == nil {
				if n.ovnnb == nil || (source.MACBindings != nil && n.ovnsb == nil) {
					err = errors.New("Original NIC backend preflight client is unavailable")
				} else {
					err = n.ovnnb.VerifyNICPortCleanup(ctx, source.Port)
					if err == nil && source.MACBindings != nil {
						err = n.ovnsb.VerifyNICMigrationMACBindings(ctx, *source.MACBindings)
					}
				}
			}
		}
	}

	if err != nil {
		return nil, errors.Join(err, release())
	}

	return release, nil
}
