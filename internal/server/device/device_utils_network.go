package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/j-keck/arping"
	"github.com/mdlayher/ndp"

	"github.com/lxc/incus/v6/internal/revert"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// Instances can be started in parallel, so lock the creation of VLANs.
var networkCreateSharedDeviceLock sync.Mutex

// NetworkSetDevMTU sets the MTU setting for a named network device if different from current.
func NetworkSetDevMTU(devName string, mtu uint32) error {
	curMTU, err := network.GetDevMTU(devName)
	if err != nil {
		return err
	}

	// Only try and change the MTU if the requested mac is different to current one.
	if curMTU != mtu {
		link := &ip.Link{Name: devName}
		err := link.SetMTU(mtu)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkGetDevMAC retrieves the current MAC setting for a named network device.
func NetworkGetDevMAC(devName string) (string, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", devName))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(content)), nil
}

// NetworkSetDevMAC sets the MAC setting for a named network device if different from current.
func NetworkSetDevMAC(devName string, mac string) error {
	curMac, err := NetworkGetDevMAC(devName)
	if err != nil {
		return err
	}

	// Only try and change the MAC if the requested mac is different to current one.
	if curMac != mac {
		hwaddr, err := net.ParseMAC(mac)
		if err != nil {
			return fmt.Errorf("Failed parsing MAC address %q: %w", mac, err)
		}

		link := &ip.Link{Name: devName}
		err = link.SetAddress(hwaddr)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkRemoveInterfaceIfNeeded removes a network interface by name but only if no other instance is using it.
func networkRemoveInterfaceIfNeeded(state *state.State, nic string, current instance.Instance, parent string, vlanID string) error {
	// Check if it's used by another instance.
	instances, err := instance.LoadNodeAll(state, instancetype.Any)
	if err != nil {
		return err
	}

	for _, inst := range instances {
		if inst.Name() == current.Name() && inst.Project().Name == current.Project().Name {
			continue
		}

		for devName, dev := range inst.ExpandedDevices() {
			if dev["type"] != "nic" || dev["vlan"] != vlanID || dev["parent"] != parent {
				continue
			}

			// Check if another running instance created the device, if so, don't touch it.
			if util.IsTrue(inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.last_state.created", devName)]) {
				return nil
			}
		}
	}

	return network.InterfaceRemove(nic)
}

// networkCreateVlanDeviceIfNeeded creates a VLAN device if doesn't already exist.
func networkCreateVlanDeviceIfNeeded(state *state.State, parent string, vlanDevice string, vlanID string, gvrp bool) (string, error) {
	if vlanID != "" {
		created, err := network.VLANInterfaceCreate(parent, vlanDevice, vlanID, gvrp)
		if err != nil {
			return "", err
		}

		if created {
			return "created", nil
		}

		// Check if it was created for another running instance.
		instances, err := instance.LoadNodeAll(state, instancetype.Any)
		if err != nil {
			return "", err
		}

		for _, inst := range instances {
			for devName, dev := range inst.ExpandedDevices() {
				if dev["type"] != "nic" || dev["vlan"] != vlanID || dev["parent"] != parent {
					continue
				}

				// Check if another running instance created the device, if so, mark it as created.
				if util.IsTrue(inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.last_state.created", devName)]) {
					return "reused", nil
				}
			}
		}
	}

	return "existing", nil
}

// networkSnapshotPhysicalNIC records properties of the NIC to volatile so they can be restored later.
func networkSnapshotPhysicalNIC(hostName string, volatile map[string]string) error {
	// Store current MTU for restoration on detach.
	mtu, err := network.GetDevMTU(hostName)
	if err != nil {
		return err
	}

	volatile["last_state.mtu"] = fmt.Sprintf("%d", mtu)

	// Store current MAC for restoration on detach
	mac, err := NetworkGetDevMAC(hostName)
	if err != nil {
		return err
	}

	volatile["last_state.hwaddr"] = mac
	return nil
}

// networkRestorePhysicalNIC restores NIC properties from volatile to what they were before it was attached.
func networkRestorePhysicalNIC(hostName string, volatile map[string]string) error {
	// If we created the "physical" device and then it should be removed.
	if util.IsTrue(volatile["last_state.created"]) {
		return network.InterfaceRemove(hostName)
	}

	// Bring the interface down, as this is sometimes needed to change settings on the nic.
	link := &ip.Link{Name: hostName}
	err := link.SetDown()
	if err != nil {
		return fmt.Errorf("Failed to bring down \"%s\": %w", hostName, err)
	}

	// If MTU value is specified then there is an original MTU that needs restoring.
	if volatile["last_state.mtu"] != "" {
		mtuInt, err := strconv.ParseUint(volatile["last_state.mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed to convert mtu for \"%s\" mtu \"%s\": %w", hostName, volatile["last_state.mtu"], err)
		}

		err = NetworkSetDevMTU(hostName, uint32(mtuInt))
		if err != nil {
			return fmt.Errorf("Failed to restore physical dev \"%s\" mtu to \"%d\": %w", hostName, mtuInt, err)
		}
	}

	// If MAC value is specified then there is an original MAC that needs restoring.
	if volatile["last_state.hwaddr"] != "" {
		err := NetworkSetDevMAC(hostName, volatile["last_state.hwaddr"])
		if err != nil {
			return fmt.Errorf("Failed to restore physical dev \"%s\" mac to \"%s\": %w", hostName, volatile["last_state.hwaddr"], err)
		}
	}

	return nil
}

// networkCreateVethPair creates and configures a veth pair. It will set the hwaddr and mtu settings
// in the supplied config to the newly created peer interface. If mtu is not specified, but parent
// is supplied in config, then the MTU of the new peer interface will inherit the parent MTU.
// Accepts the name of the host side interface as a parameter and returns the peer interface name and MTU used.
func networkCreateVethPair(hostName string, m deviceConfig.Device) (string, uint32, error) {
	var err error

	veth := &ip.Veth{
		Link: ip.Link{
			Name: hostName,
			Up:   true,
		},
		Peer: ip.Link{
			Name: network.RandomDevName("veth"),
		},
	}

	// Set the MTU on both ends.
	// The host side should always line up with the bridge to avoid accidentally lowering the bridge MTU.
	// The instance side should use the configured MTU (if any), if not, it should match the host side.
	var instanceMTU uint32
	var parentMTU uint32

	if m["parent"] != "" {
		mtu, err := network.GetDevMTU(m["parent"])
		if err != nil {
			return "", 0, fmt.Errorf("Failed to get the parent MTU: %w", err)
		}

		parentMTU = uint32(mtu)
	}

	if m["mtu"] != "" {
		mtu, err := strconv.ParseUint(m["mtu"], 10, 32)
		if err != nil {
			return "", 0, fmt.Errorf("Invalid MTU specified: %w", err)
		}

		instanceMTU = uint32(mtu)
	}

	if instanceMTU == 0 && parentMTU > 0 {
		instanceMTU = parentMTU
	}

	if parentMTU == 0 && instanceMTU > 0 {
		parentMTU = instanceMTU
	}

	if instanceMTU > 0 {
		veth.Peer.MTU = instanceMTU
	}

	if parentMTU > 0 {
		veth.MTU = parentMTU
	}

	// Set the MAC address on peer.
	if m["hwaddr"] != "" {
		hwaddr, err := net.ParseMAC(m["hwaddr"])
		if err != nil {
			return "", 0, fmt.Errorf("Failed parsing MAC address %q: %w", m["hwaddr"], err)
		}

		veth.Peer.Address = hwaddr
	}

	// Set TX queue length on both ends.
	if m["queue.tx.length"] != "" {
		nicTXqlen, err := strconv.ParseUint(m["queue.tx.length"], 10, 32)
		if err != nil {
			return "", 0, fmt.Errorf("Invalid txqueuelen specified: %w", err)
		}

		veth.TXQueueLength = uint32(nicTXqlen)
	} else if m["parent"] != "" {
		veth.TXQueueLength, err = network.GetTXQueueLength(m["parent"])
		if err != nil {
			return "", 0, fmt.Errorf("Failed to get the parent txqueuelen: %w", err)
		}
	}

	veth.Peer.TXQueueLength = veth.TXQueueLength

	// Add and configure the interface in one operation to reduce the number of executions and to avoid
	// systemd-udevd from applying the default MACAddressPolicy=persistent policy.
	err = veth.Add()
	if err != nil {
		return "", 0, fmt.Errorf("Failed to create the veth interfaces %q and %q: %w", hostName, veth.Peer.Name, err)
	}

	return veth.Peer.Name, veth.Peer.MTU, nil
}

// networkCreateTap creates and configures a TAP device.
// Returns the MTU used.
func networkCreateTap(hostName string, m deviceConfig.Device) (uint32, error) {
	tuntap := &ip.Tuntap{
		Name:       hostName,
		Mode:       "tap",
		MultiQueue: true,
	}

	err := tuntap.Add()
	if err != nil {
		return 0, fmt.Errorf("Failed to create the tap interfaces %q: %w", hostName, err)
	}

	revert := revert.New()
	defer revert.Fail()

	link := &ip.Link{Name: hostName}
	err = link.SetUp()
	if err != nil {
		return 0, fmt.Errorf("Failed to bring up the tap interface %q: %w", hostName, err)
	}

	revert.Add(func() { _ = network.InterfaceRemove(hostName) })

	// Set the MTU on both ends.
	// The host side should always line up with the bridge to avoid accidentally lowering the bridge MTU.
	// The instance side should use the configured MTU (if any), if not, it should match the host side.
	var mtu uint32
	if m["mtu"] != "" {
		nicMTU, err := strconv.ParseUint(m["mtu"], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("Invalid MTU specified: %w", err)
		}

		mtu = uint32(nicMTU)
	}

	if m["parent"] != "" {
		parentMTU, err := network.GetDevMTU(m["parent"])
		if err != nil {
			return 0, fmt.Errorf("Failed to get the parent MTU: %w", err)
		}

		err = NetworkSetDevMTU(hostName, parentMTU)
		if err != nil {
			return 0, fmt.Errorf("Failed to set the MTU %d: %w", mtu, err)
		}

		if mtu == 0 {
			mtu = parentMTU
		}
	}

	// Set TX queue length on both ends.
	var txqueuelen uint32
	if m["queue.tx.length"] != "" {
		nicTXqlen, err := strconv.ParseUint(m["queue.tx.length"], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("Invalid txqueuelen specified: %w", err)
		}

		txqueuelen = uint32(nicTXqlen)
	} else if m["parent"] != "" {
		txqueuelen, err = network.GetTXQueueLength(m["parent"])
		if err != nil {
			return 0, fmt.Errorf("Failed to get the parent txqueuelen: %w", err)
		}
	}

	if txqueuelen > 0 {
		err = link.SetTXQueueLength(txqueuelen)
		if err != nil {
			return 0, fmt.Errorf("Failed to set the TX queue length %d: %w", txqueuelen, err)
		}
	}

	revert.Success()
	return mtu, nil
}

// networkVethFillFromVolatile fills veth host_name and hwaddr fields from volatile if not set in device config.
func networkVethFillFromVolatile(device deviceConfig.Device, volatile map[string]string) {
	// If not configured, check if volatile data contains the most recently added host_name.
	if device["host_name"] == "" {
		device["host_name"] = volatile["host_name"]
	}

	// If not configured, check if volatile data contains the most recently added hwaddr.
	if device["hwaddr"] == "" {
		device["hwaddr"] = volatile["hwaddr"]
	}
}

// networkNICRouteAdd applies any static host-side routes configured for an instance NIC.
func networkNICRouteAdd(routeDev string, routes ...string) error {
	if !network.InterfaceExists(routeDev) {
		return fmt.Errorf("Route interface missing %q", routeDev)
	}

	revert := revert.New()
	defer revert.Fail()

	for _, r := range routes {
		route := r // Local var for revert.
		ipAddress, _, err := net.ParseCIDR(route)
		if err != nil {
			return fmt.Errorf("Invalid route %q: %w", route, err)
		}

		ipVersion := ip.FamilyV4
		if ipAddress.To4() == nil {
			ipVersion = ip.FamilyV6
		}

		// Add IP route (using boot proto to avoid conflicts with network defined static routes).
		r := &ip.Route{
			DevName: routeDev,
			Route:   route,
			Proto:   "boot",
			Family:  ipVersion,
		}

		err = r.Add()
		if err != nil {
			return err
		}

		revert.Add(func() {
			r := &ip.Route{
				DevName: routeDev,
				Route:   route,
				Proto:   "boot",
				Family:  ipVersion,
			}

			_ = r.Flush()
		})
	}

	revert.Success()
	return nil
}

// networkNICRouteDelete deletes any static host-side routes configured for an instance NIC.
// Logs any errors and continues to next route to remove.
func networkNICRouteDelete(routeDev string, routes ...string) {
	if routeDev == "" {
		logger.Errorf("Failed removing static route, empty route device specified")
		return
	}

	if !network.InterfaceExists(routeDev) {
		return //Routes will already be gone if device doesn't exist.
	}

	for _, r := range routes {
		route := r // Local var for revert.
		ipAddress, _, err := net.ParseCIDR(route)
		if err != nil {
			logger.Errorf("Failed to remove static route %q to %q: %v", route, routeDev, err)
			continue
		}

		ipVersion := ip.FamilyV4
		if ipAddress.To4() == nil {
			ipVersion = ip.FamilyV6
		}

		// Add IP route (using boot proto to avoid conflicts with network defined static routes).
		r := &ip.Route{
			DevName: routeDev,
			Route:   route,
			Proto:   "boot",
			Family:  ipVersion,
		}

		err = r.Flush()
		if err != nil {
			logger.Errorf("Failed to remove static route %q to %q: %v", route, routeDev, err)
			continue
		}
	}
}

// networkSetupHostVethLimits applies any network rate limits to the veth device specified in the config.
func networkSetupHostVethLimits(d *deviceCommon, oldConfig deviceConfig.Device, bridged bool) error {
	var err error

	veth := d.config["host_name"]

	if veth == "" || !network.InterfaceExists(veth) {
		return fmt.Errorf("Unknown or missing host side veth device %q", veth)
	}

	// Apply max limit
	if d.config["limits.max"] != "" {
		d.config["limits.ingress"] = d.config["limits.max"]
		d.config["limits.egress"] = d.config["limits.max"]
	}

	// Parse the values
	var ingressInt int64
	if d.config["limits.ingress"] != "" {
		ingressInt, err = units.ParseBitSizeString(d.config["limits.ingress"])
		if err != nil {
			return err
		}
	}

	var egressInt int64
	if d.config["limits.egress"] != "" {
		egressInt, err = units.ParseBitSizeString(d.config["limits.egress"])
		if err != nil {
			return err
		}
	}

	// Clean any existing entry
	qdisc := &ip.Qdisc{Dev: veth, Root: true}
	_ = qdisc.Delete()
	qdisc = &ip.Qdisc{Dev: veth, Ingress: true}
	_ = qdisc.Delete()

	// Apply new limits
	if d.config["limits.ingress"] != "" {
		qdiscHTB := &ip.QdiscHTB{Qdisc: ip.Qdisc{Dev: veth, Handle: "1:0", Root: true}, Default: "10"}
		err := qdiscHTB.Add()
		if err != nil {
			return fmt.Errorf("Failed to create root tc qdisc: %s", err)
		}

		classHTB := &ip.ClassHTB{Class: ip.Class{Dev: veth, Parent: "1:0", Classid: "1:10"}, Rate: fmt.Sprintf("%dbit", ingressInt)}
		err = classHTB.Add()
		if err != nil {
			return fmt.Errorf("Failed to create limit tc class: %s", err)
		}

		filter := &ip.U32Filter{Filter: ip.Filter{Dev: veth, Parent: "1:0", Protocol: "all", Flowid: "1:1"}, Value: "0", Mask: "0"}
		err = filter.Add()
		if err != nil {
			return fmt.Errorf("Failed to create tc filter: %s", err)
		}
	}

	if d.config["limits.egress"] != "" {
		qdisc = &ip.Qdisc{Dev: veth, Handle: "ffff:0", Ingress: true}
		err := qdisc.Add()
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", err)
		}

		police := &ip.ActionPolice{Rate: fmt.Sprintf("%dbit", egressInt), Burst: "1024k", Mtu: "64kb", Drop: true}
		filter := &ip.U32Filter{Filter: ip.Filter{Dev: veth, Parent: "ffff:0", Protocol: "all"}, Value: "0", Mask: "0", Actions: []ip.Action{police}}
		err = filter.Add()
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc filter: %s", err)
		}
	}

	var networkPriority uint64
	if d.config["limits.priority"] != "" {
		networkPriority, err = strconv.ParseUint(d.config["limits.priority"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed to parse limits.priority %q: %w", d.config["limits.priority"], err)
		}
	}

	if oldConfig != nil && oldConfig["limits.priority"] != d.config["limits.priority"] {
		err = d.state.Firewall.InstanceClearNetPrio(d.inst.Project().Name, d.inst.Name(), veth)
		if err != nil {
			return err
		}
	}

	if oldConfig == nil || (oldConfig != nil && oldConfig["limits.priority"] != d.config["limits.priority"]) {
		if networkPriority != 0 {
			if bridged && d.state.Firewall.String() == "xtables" {
				return fmt.Errorf("Failed to setup instance device network priority. The xtables firewall driver does not support required functionality.")
			}

			err = d.state.Firewall.InstanceSetupNetPrio(d.inst.Project().Name, d.inst.Name(), veth, uint32(networkPriority))
			if err != nil {
				return fmt.Errorf("Failed to setup instance device network priority: %w", err)
			}
		}
	}

	return nil
}

// networkClearHostVethLimits clears any network rate limits to the veth device specified in the config.
func networkClearHostVethLimits(d *deviceCommon) error {
	err := d.state.Firewall.InstanceClearNetPrio(d.inst.Project().Name, d.inst.Name(), d.config["host_name"])
	if err != nil {
		return err
	}

	return nil
}

// networkValidGateway validates the gateway value.
func networkValidGateway(value string) error {
	if slices.Contains([]string{"none", "auto"}, value) {
		return nil
	}

	return fmt.Errorf("Invalid gateway: %s", value)
}

// bgpAddPrefix adds external routes to the BGP server.
func bgpAddPrefix(d *deviceCommon, n network.Network, config map[string]string) error {
	// BGP is only valid when tied to a managed network.
	if config["network"] == "" {
		return nil
	}

	// Parse nexthop configuration.
	nexthopV4 := net.ParseIP(n.Config()["bgp.ipv4.nexthop"])
	if nexthopV4 == nil {
		nexthopV4 = net.ParseIP(n.Config()["volatile.network.ipv4.address"])
		if nexthopV4 == nil {
			nexthopV4 = net.ParseIP("0.0.0.0")
		}
	}

	nexthopV6 := net.ParseIP(n.Config()["bgp.ipv6.nexthop"])
	if nexthopV6 == nil {
		nexthopV6 = net.ParseIP(n.Config()["volatile.network.ipv6.address"])
		if nexthopV6 == nil {
			nexthopV6 = net.ParseIP("::")
		}
	}

	// Add the prefixes.
	bgpOwner := fmt.Sprintf("instance_%d_%s", d.inst.ID(), d.name)
	if config["ipv4.routes.external"] != "" {
		for _, prefix := range util.SplitNTrimSpace(config["ipv4.routes.external"], ",", -1, true) {
			_, prefixNet, err := net.ParseCIDR(prefix)
			if err != nil {
				return err
			}

			err = d.state.BGP.AddPrefix(*prefixNet, nexthopV4, bgpOwner)
			if err != nil {
				return err
			}
		}
	}

	if config["ipv6.routes.external"] != "" {
		for _, prefix := range util.SplitNTrimSpace(config["ipv6.routes.external"], ",", -1, true) {
			_, prefixNet, err := net.ParseCIDR(prefix)
			if err != nil {
				return err
			}

			err = d.state.BGP.AddPrefix(*prefixNet, nexthopV6, bgpOwner)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func bgpRemovePrefix(d *deviceCommon, config map[string]string) error {
	// BGP is only valid when tied to a managed network.
	if config["network"] == "" {
		return nil
	}

	// Load the network configuration.
	err := d.state.BGP.RemovePrefixByOwner(fmt.Sprintf("instance_%d_%s", d.inst.ID(), d.name))
	if err != nil {
		return err
	}

	return nil
}

// networkSRIOVParentVFInfo returns info about an SR-IOV virtual function from the parent NIC using the ip tool.
func networkSRIOVParentVFInfo(vfParent string, vfID int) (ip.VirtFuncInfo, error) {
	link := &ip.Link{Name: vfParent}
	vfi, err := link.GetVFInfo(vfID)
	return vfi, err
}

// networkSRIOVSetupVF configures a SR-IOV virtual function (VF) on the parent (PF) and stores original properties
// of the PF and VF devices into voltatile for restoration on detach.
// The useSpoofCheck argument controls whether to use the spoof check feature for the VF on the parent device.
// If this is false then "security.mac_filtering" must not be enabled.
// Returns VF PCI device info and IOMMU group number for VMs.
func networkSRIOVSetupVF(d deviceCommon, vfParent string, vfDevice string, vfID int, useSpoofCheck bool, volatile map[string]string) (pcidev.Device, uint64, error) {
	var vfPCIDev pcidev.Device

	// Retrieve VF settings from parent device.
	vfInfo, err := networkSRIOVParentVFInfo(vfParent, vfID)
	if err != nil {
		return vfPCIDev, 0, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Record properties of VF settings on the parent device.
	volatile["last_state.vf.parent"] = vfParent
	volatile["last_state.vf.hwaddr"] = vfInfo.Address
	volatile["last_state.vf.id"] = fmt.Sprintf("%d", vfID)
	volatile["last_state.vf.vlan"] = fmt.Sprintf("%d", vfInfo.VLANs[0]["vlan"])
	volatile["last_state.vf.spoofcheck"] = fmt.Sprintf("%t", vfInfo.SpoofCheck)

	// Record the host interface we represents the VF device which we will move into instance.
	volatile["host_name"] = vfDevice
	volatile["last_state.created"] = "false" // Indicates don't delete device at stop time.

	// Record properties of VF device.
	err = networkSnapshotPhysicalNIC(volatile["host_name"], volatile)
	if err != nil {
		return vfPCIDev, 0, fmt.Errorf("Failed recording NIC %q settings: %w", volatile["host_name"], err)
	}

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCIDev, err = network.SRIOVGetVFDevicePCISlot(vfParent, volatile["last_state.vf.id"])
	if err != nil {
		return vfPCIDev, 0, fmt.Errorf("Failed getting PCI slot for VF %q: %w", volatile["last_state.vf.id"], err)
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = pcidev.DeviceUnbind(vfPCIDev)
	if err != nil {
		return vfPCIDev, 0, err
	}

	revert.Add(func() { _ = pcidev.DeviceProbe(vfPCIDev) })

	// Setup VF VLAN if specified.
	if d.config["vlan"] != "" {
		link := &ip.Link{Name: vfParent}
		err := link.SetVfVlan(volatile["last_state.vf.id"], d.config["vlan"])
		if err != nil {
			return vfPCIDev, 0, fmt.Errorf("Failed setting VLAN for VF %q: %w", volatile["last_state.vf.id"], err)
		}
	}

	// Setup VF MAC spoofing protection if specified.
	// The ordering of this section is very important, as Intel cards require a very specific
	// order of setup to allow setting custom MACs when using spoof check mode.
	if util.IsTrue(d.config["security.mac_filtering"]) {
		if !useSpoofCheck {
			return pcidev.Device{}, 0, fmt.Errorf("security.mac_filtering cannot be enabled when VF spoof check not enabled")
		}

		// If no MAC specified in config, use current VF interface MAC.
		mac := d.config["hwaddr"]
		if mac == "" {
			mac = volatile["last_state.hwaddr"]
		}

		// Set MAC on VF (this combined with spoof checking prevents any other MAC being used).
		link := &ip.Link{Name: vfParent}
		err = link.SetVfAddress(volatile["last_state.vf.id"], mac)
		if err != nil {
			return vfPCIDev, 0, fmt.Errorf("Failed setting MAC for VF %q: %w", volatile["last_state.vf.id"], err)
		}

		// Now that MAC is set on VF, we can enable spoof checking.
		err = link.SetVfSpoofchk(volatile["last_state.vf.id"], "on")
		if err != nil {
			return vfPCIDev, 0, fmt.Errorf("Failed enabling spoof check for VF %q: %w", volatile["last_state.vf.id"], err)
		}
	} else {
		// Try to reset VF to ensure no previous MAC restriction exists, as some devices require this
		// before being able to set a new VF MAC or disable spoofchecking. However some devices don't
		// allow it so ignore failures.
		link := &ip.Link{Name: vfParent}
		_ = link.SetVfAddress(volatile["last_state.vf.id"], "00:00:00:00:00:00")

		if useSpoofCheck {
			// Ensure spoof checking is disabled if not enabled in instance (only for real VF).
			err = link.SetVfSpoofchk(volatile["last_state.vf.id"], "off")
			if err != nil {
				return vfPCIDev, 0, fmt.Errorf("Failed disabling spoof check for VF %q: %w", volatile["last_state.vf.id"], err)
			}
		}

		// Set MAC on VF if specified (this should be passed through into VM when it is bound to vfio-pci).
		if d.inst.Type() == instancetype.VM {
			// If no MAC specified in config, use current VF interface MAC.
			mac := d.config["hwaddr"]
			if mac == "" {
				mac = volatile["last_state.hwaddr"]
			}

			err = link.SetVfAddress(volatile["last_state.vf.id"], mac)
			if err != nil {
				return vfPCIDev, 0, fmt.Errorf("Failed setting MAC for VF %q: %w", volatile["last_state.vf.id"], err)
			}
		}
	}

	// pciIOMMUGroup, used for VM physical passthrough.
	var pciIOMMUGroup uint64

	if d.inst.Type() == instancetype.Container {
		// Bind VF device onto the host so that the settings will take effect.
		err = networkPCIBindWaitInterface(vfPCIDev, volatile["host_name"])
		if err != nil {
			return vfPCIDev, 0, err
		}
	} else if d.inst.Type() == instancetype.VM {
		pciIOMMUGroup, err = pcidev.DeviceIOMMUGroup(vfPCIDev.SlotName)
		if err != nil {
			return vfPCIDev, 0, fmt.Errorf("Failed getting IOMMU group for VF device %q: %w", vfPCIDev.SlotName, err)
		}

		if d.config["acceleration"] != "vdpa" {
			// Register VF device with vfio-pci driver so it can be passed to VM.
			err = pcidev.DeviceDriverOverride(vfPCIDev, "vfio-pci")
			if err != nil {
				return vfPCIDev, 0, fmt.Errorf("Failed overriding driver for VF device %q: %w", vfPCIDev.SlotName, err)
			}
		} else {
			// Bind VF device onto the host so that the settings will take effect.
			err = networkPCIBindWaitInterface(vfPCIDev, volatile["host_name"])
			if err != nil {
				return vfPCIDev, 0, err
			}
		}

		// Record original driver used by VF device for restore.
		volatile["last_state.pci.driver"] = vfPCIDev.Driver
	}

	revert.Success()
	return vfPCIDev, pciIOMMUGroup, nil
}

// networkSRIOVRestoreVF restores SR-IOV VF device settings on parent PF and on VF NIC. Used when removing a VF NIC
// from an instance. Use volatile data that was stored when the device was first added with networkSRIOVSetupVF().
// The useSpoofCheck argument controls whether to use the spoof check feature for the VF on the parent device.
func networkSRIOVRestoreVF(d deviceCommon, useSpoofCheck bool, volatile map[string]string) error {
	// Retrieve parent interface from config or volatile.
	parent := d.config["parent"]
	if parent == "" {
		parent = volatile["last_state.vf.parent"]
	}

	// Nothing to do if we don't know the original device name or the VF ID.
	if volatile["host_name"] == "" || volatile["last_state.vf.id"] == "" || parent == "" {
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Get VF device's PCI info so we can unbind and rebind it from the host.
	vfPCIDev, err := network.SRIOVGetVFDevicePCISlot(parent, volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the restored settings will take effect when we rebind it.
	err = pcidev.DeviceUnbind(vfPCIDev)
	if err != nil {
		return err
	}

	if d.inst.Type() == instancetype.VM {
		// Before we bind the device back to the host, ensure we restore the original driver info as it
		// should be currently set to vfio-pci.
		err = pcidev.DeviceSetDriverOverride(vfPCIDev, volatile["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	revert.Add(func() { _ = pcidev.DeviceProbe(vfPCIDev) })

	// Reset VF VLAN if specified
	if volatile["last_state.vf.vlan"] != "" {
		link := &ip.Link{Name: parent}
		err := link.SetVfVlan(volatile["last_state.vf.id"], volatile["last_state.vf.vlan"])
		if err != nil {
			return err
		}
	}

	// Reset VF MAC spoofing protection if recorded. Do this first before resetting the MAC
	// to avoid any issues with zero MACs refusing to be set whilst spoof check is on.
	if useSpoofCheck && volatile["last_state.vf.spoofcheck"] != "" {
		mode := "off"
		if util.IsTrue(volatile["last_state.vf.spoofcheck"]) {
			mode = "on"
		}

		link := &ip.Link{Name: parent}
		err := link.SetVfSpoofchk(volatile["last_state.vf.id"], mode)
		if err != nil {
			return err
		}
	}

	// Reset VF MAC specified if specified.
	if volatile["last_state.vf.hwaddr"] != "" {
		link := &ip.Link{Name: parent}
		err := link.SetVfAddress(volatile["last_state.vf.id"], volatile["last_state.vf.hwaddr"])
		if err != nil {
			return err
		}
	}

	// Bind VF device onto the host so that the settings will take effect.
	err = networkPCIBindWaitInterface(vfPCIDev, volatile["host_name"])
	if err != nil {
		return err
	}

	// Restore VF interface settings.
	err = networkRestorePhysicalNIC(volatile["host_name"], volatile)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// networkPCIBindWaitInterface repeatedly requests the pciDev is probed to be bound to the override driver and
// checks whether the expected network interface has appeared as the result of the device driver being bound.
func networkPCIBindWaitInterface(pciDev pcidev.Device, ifName string) error {
	var err error

	waitDuration := time.Second * 10
	waitUntil := time.Now().Add(waitDuration)

	// Keep requesting the device driver be probed in case it was not ready previously or the expected
	// interface has not appeared yet. The device can be probed multiple times safely.

	i := 0
	for {
		err = pcidev.DeviceProbe(pciDev)
		if err == nil && network.InterfaceExists(ifName) {
			return nil
		}

		if time.Now().After(waitUntil) {
			if err != nil {
				return fmt.Errorf("Failed binding interface %q after %v: %w", ifName, waitDuration, err)
			}

			return fmt.Errorf("Failed binding interface %q after %v", ifName, waitDuration)
		}

		if i <= 5 {
			// Retry more quickly early on.
			time.Sleep(time.Millisecond * time.Duration(i) * 10)
		} else {
			time.Sleep(time.Second)
		}

		i++
	}
}

// networkSRIOVSetupContainerVFNIC configures the VF NIC interface ready for moving into container.
// It configures the MAC address and MTU, then brings the interface up.
func networkSRIOVSetupContainerVFNIC(hostName string, config map[string]string) error {
	// Set the MAC address.
	if config["hwaddr"] != "" {
		hwaddr, err := net.ParseMAC(config["hwaddr"])
		if err != nil {
			return fmt.Errorf("Failed parsing MAC address %q: %w", config["hwaddr"], err)
		}

		link := &ip.Link{Name: hostName}
		err = link.SetAddress(hwaddr)
		if err != nil {
			return fmt.Errorf("Failed setting MAC address %q on %q: %w", config["hwaddr"], hostName, err)
		}
	}

	// Set the MTU.
	if config["mtu"] != "" {
		mtu, err := strconv.ParseUint(config["mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Invalid VF MTU specified %q: %w", config["mtu"], err)
		}

		link := &ip.Link{Name: hostName}
		err = link.SetMTU(uint32(mtu))
		if err != nil {
			return fmt.Errorf("Failed setting MTU %q on %q: %w", config["mtu"], hostName, err)
		}
	}

	// Bring the interface up.
	link := &ip.Link{Name: hostName}
	err := link.SetUp()
	if err != nil {
		if config["hwaddr"] != "" {
			return fmt.Errorf("Failed to bring up VF interface %q: %w", hostName, err)
		}

		upErr := err

		// If interface fails to come up and MAC not previously set, some NICs require us to set
		// a specific MAC before being allowed to bring up the VF interface. So check if interface
		// has an empty MAC and set a random one if needed.
		vfIF, err := net.InterfaceByName(hostName)
		if err != nil {
			return fmt.Errorf("Failed getting interface info for VF %q: %w", hostName, err)
		}

		// If the VF interface has a MAC already, something else prevented bringing interface up.
		if vfIF.HardwareAddr.String() != "00:00:00:00:00:00" {
			return fmt.Errorf("Failed to bring up VF interface %q: %w", hostName, upErr)
		}

		// Try using a random MAC address and bringing interface up.
		randMAC, err := instance.DeviceNextInterfaceHWAddr()
		if err != nil {
			return fmt.Errorf("Failed generating random MAC for VF %q: %w", hostName, err)
		}

		hwaddr, err := net.ParseMAC(randMAC)
		if err != nil {
			return fmt.Errorf("Failed parsing MAC address %q: %w", randMAC, err)
		}

		link := &ip.Link{Name: hostName}
		err = link.SetAddress(hwaddr)
		if err != nil {
			return fmt.Errorf("Failed to set random MAC address %q on %q: %w", randMAC, hostName, err)
		}

		err = link.SetUp()
		if err != nil {
			return fmt.Errorf("Failed to bring up VF interface %q: %w", hostName, err)
		}
	}

	return nil
}

// isIPAvailable checks if address responds to ARP/NDP neighbour probe on the parentInterface.
// Returns true if IP is available.
func isIPAvailable(ctx context.Context, address net.IP, parentInterface string) (bool, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		// Set default timeout of 500ms if no deadline context provided.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(500*time.Millisecond))
		defer cancel()
		deadline, _ = ctx.Deadline()
	}

	// Handle IPv4 address.
	if address.To4() != nil {
		timeout := time.Until(deadline)
		arping.SetTimeout(timeout)
		_, _, err := arping.PingOverIfaceByName(address, parentInterface)
		if err != nil {
			if errors.Is(err, arping.ErrTimeout) {
				return false, nil
			}

			return false, err
		}

		return true, nil
	}

	// Handle IPv6 address.
	networkInterface, err := net.InterfaceByName(parentInterface)
	if err != nil {
		return false, err
	}

	conn, _, err := ndp.Listen(networkInterface, ndp.LinkLocal)
	if err != nil {
		return false, err
	}

	defer func() { _ = conn.Close() }()

	netipAddr, ok := netip.AddrFromSlice(address)
	if !ok {
		return false, fmt.Errorf("Couldn't convert address to netip")
	}

	solicitedNodeMulticast, err := ndp.SolicitedNodeMulticast(netipAddr)
	if err != nil {
		return false, err
	}

	neighbourSolicitationMessage := &ndp.NeighborSolicitation{
		TargetAddress: netipAddr,
	}

	_ = conn.SetDeadline(deadline)
	err = conn.WriteTo(neighbourSolicitationMessage, nil, solicitedNodeMulticast)
	if err != nil {
		return false, err
	}

	_ = conn.SetDeadline(deadline)
	msg, _, _, err := conn.ReadFrom()
	if err != nil {
		cause, ok := err.(net.Error)
		if ok && cause.Timeout() {
			return false, nil
		}

		return false, err
	}

	neighbourAdvertisement, ok := msg.(*ndp.NeighborAdvertisement)
	if ok && neighbourAdvertisement.TargetAddress == netipAddr {
		return true, nil
	}

	return false, nil
}

// networkVLANListExpand takes in a list of raw VLAN values (string) that includes
// different VLAN formats ("number" and "start-end") and convert them into a list of
// expanded VLAN values in integer.
func networkVLANListExpand(rawVLANValues []string) ([]int, error) {
	var networkVLANList []int
	for _, vlan := range rawVLANValues {
		start, count, err := validate.ParseNetworkVLANRange(vlan)
		if err != nil {
			return nil, err
		}

		for i := start; i < start+count; i++ {
			networkVLANList = append(networkVLANList, i)
		}
	}

	return networkVLANList, nil
}
