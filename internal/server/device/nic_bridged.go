package device

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/netx/eui64"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/dnsmasq"
	"github.com/lxc/incus/internal/server/dnsmasq/dhcpalloc"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/network/ovs"
	"github.com/lxc/incus/internal/server/resources"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

type bridgeNetwork interface {
	UsesDNSMasq() bool
}

type nicBridged struct {
	deviceCommon

	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicBridged) CanHotPlug() bool {
	return true
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicBridged) CanMigrate() bool {
	return d.network != nil
}

// validateConfig checks the supplied config for correctness.
func (d *nicBridged) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	var requiredFields []string
	optionalFields := []string{
		"name",
		"network",
		"parent",
		"mtu",
		"queue.tx.length",
		"hwaddr",
		"host_name",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"limits.priority",
		"ipv4.address",
		"ipv6.address",
		"ipv4.routes",
		"ipv6.routes",
		"ipv4.routes.external",
		"ipv6.routes.external",
		"security.mac_filtering",
		"security.ipv4_filtering",
		"security.ipv6_filtering",
		"security.port_isolation",
		"boot.priority",
		"vlan",
	}

	// checkWithManagedNetwork validates the device's settings against the managed network.
	checkWithManagedNetwork := func(n network.Network) error {
		if n.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Specified network is not fully created")
		}

		if n.Type() != "bridge" {
			return fmt.Errorf("Specified network must be of type bridge")
		}

		netConfig := n.Config()

		if d.config["ipv4.address"] != "" {
			dhcpv4Subnet := n.DHCPv4Subnet()

			// Check that DHCPv4 is enabled on parent network (needed to use static assigned IPs) when
			// IP filtering isn't enabled (if it is we allow the use of static IPs for this purpose).
			if dhcpv4Subnet == nil && util.IsFalseOrEmpty(d.config["security.ipv4_filtering"]) {
				return fmt.Errorf(`Cannot specify "ipv4.address" when DHCP is disabled (unless using security.ipv4_filtering) on network %q`, n.Name())
			}

			// Check the static IP supplied is valid for the linked network. It should be part of the
			// network's subnet, but not necessarily part of the dynamic allocation ranges.
			if dhcpv4Subnet != nil && d.config["ipv4.address"] != "none" && !dhcpalloc.DHCPValidIP(dhcpv4Subnet, nil, net.ParseIP(d.config["ipv4.address"])) {
				return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv4.address"], n.Name())
			}

			parentAddress := netConfig["ipv4.address"]
			if slices.Contains([]string{"", "none"}, parentAddress) {
				return nil
			}

			ip, _, err := net.ParseCIDR(parentAddress)
			if err != nil {
				return fmt.Errorf("Invalid network ipv4.address: %w", err)
			}

			if d.config["ipv4.address"] == "none" && util.IsFalseOrEmpty(d.config["security.ipv4_filtering"]) {
				return fmt.Errorf("Cannot have ipv4.address as none unless using security.ipv4_filtering")
			}

			// IP should not be the same as the parent managed network address.
			if ip.Equal(net.ParseIP(d.config["ipv4.address"])) {
				return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv4.address"], d.config["parent"])
			}
		}

		if d.config["ipv6.address"] != "" {
			dhcpv6Subnet := n.DHCPv6Subnet()

			// Check that DHCPv6 is enabled on parent network (needed to use static assigned IPs) when
			// IP filtering isn't enabled (if it is we allow the use of static IPs for this purpose).
			if (dhcpv6Subnet == nil || util.IsFalseOrEmpty(netConfig["ipv6.dhcp.stateful"])) && util.IsFalseOrEmpty(d.config["security.ipv6_filtering"]) {
				return fmt.Errorf(`Cannot specify "ipv6.address" when DHCP or "ipv6.dhcp.stateful" are disabled (unless using security.ipv6_filtering) on network %q`, n.Name())
			}

			// Check the static IP supplied is valid for the linked network. It should be part of the
			// network's subnet, but not necessarily part of the dynamic allocation ranges.
			if dhcpv6Subnet != nil && d.config["ipv6.address"] != "none" && !dhcpalloc.DHCPValidIP(dhcpv6Subnet, nil, net.ParseIP(d.config["ipv6.address"])) {
				return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv6.address"], n.Name())
			}

			parentAddress := netConfig["ipv6.address"]
			if slices.Contains([]string{"", "none"}, parentAddress) {
				return nil
			}

			ip, _, err := net.ParseCIDR(parentAddress)
			if err != nil {
				return fmt.Errorf("Invalid network ipv6.address: %w", err)
			}

			if d.config["ipv6.address"] == "none" && util.IsFalseOrEmpty(d.config["security.ipv6_filtering"]) {
				return fmt.Errorf("Cannot have ipv6.address as none unless using security.ipv6_filtering")
			}

			// IP should not be the same as the parent managed network address.
			if ip.Equal(net.ParseIP(d.config["ipv6.address"])) {
				return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv6.address"], d.config["parent"])
			}
		}

		// When we know the parent network is managed, we can validate the NIC's VLAN settings based on
		// on the bridge driver type.
		if slices.Contains([]string{"", "native"}, netConfig["bridge.driver"]) {
			// Check VLAN 0 isn't set when using a native Linux managed bridge, as not supported.
			if d.config["vlan"] == "0" {
				return fmt.Errorf("VLAN ID 0 is not allowed for native Linux bridges")
			}

			// Check that none of the supplied VLAN IDs are VLAN 0 when using a native Linux managed
			// bridge, as not supported.
			networkVLANList, err := networkVLANListExpand(util.SplitNTrimSpace(d.config["vlan.tagged"], ",", -1, true))
			if err != nil {
				return err
			}

			for _, vlanID := range networkVLANList {
				if vlanID == 0 {
					return fmt.Errorf("VLAN tagged ID 0 is not allowed for native Linux bridges")
				}
			}
		}

		return nil
	}

	// Check that if network proeperty is set that conflicting keys are not present.
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// Load managed network. api.ProjectDefaultName is used here as bridge networks don't support projects.
		var err error
		d.network, err = network.LoadByName(d.state, api.ProjectDefaultName, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		// Validate NIC settings with managed network.
		err = checkWithManagedNetwork(d.network)
		if err != nil {
			return err
		}

		// Apply network settings to NIC.
		netConfig := d.network.Config()

		// Link device to network bridge.
		d.config["parent"] = d.config["network"]

		// Apply network level config options to device config before validation.
		if netConfig["bridge.mtu"] != "" {
			d.config["mtu"] = netConfig["bridge.mtu"]
		}
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")

		// Check if parent is a managed network.
		// api.ProjectDefaultName is used here as bridge networks don't support projects.
		d.network, _ = network.LoadByName(d.state, api.ProjectDefaultName, d.config["parent"])
		if d.network != nil {
			// Validate NIC settings with managed network.
			err := checkWithManagedNetwork(d.network)
			if err != nil {
				return err
			}
		} else {
			// Check that static IPs are only specified with IP filtering when using an unmanaged
			// parent bridge.
			if util.IsTrue(d.config["security.ipv4_filtering"]) {
				if d.config["ipv4.address"] == "" {
					return fmt.Errorf("IPv4 filtering requires a manually specified ipv4.address when using an unmanaged parent bridge")
				}
			} else if d.config["ipv4.address"] != "" {
				// Static IP cannot be used with unmanaged parent.
				return fmt.Errorf("Cannot use manually specified ipv4.address when using unmanaged parent bridge")
			}

			if util.IsTrue(d.config["security.ipv6_filtering"]) {
				if d.config["ipv6.address"] == "" {
					return fmt.Errorf("IPv6 filtering requires a manually specified ipv6.address when using an unmanaged parent bridge")
				}
			} else if d.config["ipv6.address"] != "" {
				// Static IP cannot be used with unmanaged parent.
				return fmt.Errorf("Cannot use manually specified ipv6.address when using unmanaged parent bridge")
			}
		}
	}

	// Check that IP filtering isn't being used with VLAN filtering.
	if util.IsTrue(d.config["security.ipv4_filtering"]) || util.IsTrue(d.config["security.ipv6_filtering"]) {
		if d.config["vlan"] != "" || d.config["vlan.tagged"] != "" {
			return fmt.Errorf("IP filtering cannot be used with VLAN filtering")
		}
	}

	// Check there isn't another NIC with any of the same addresses specified on the same cluster member.
	// Can only validate this when the instance is supplied (and not doing profile validation).
	if d.inst != nil {
		err := d.checkAddressConflict()
		if err != nil {
			return err
		}
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	// Add bridge specific vlan validation.
	rules["vlan"] = func(value string) error {
		if value == "" || value == "none" {
			return nil
		}

		return validate.IsNetworkVLAN(value)
	}

	// Add bridge specific vlan.tagged validation.
	rules["vlan.tagged"] = func(value string) error {
		if value == "" {
			return nil
		}

		// Check that none of the supplied VLAN IDs are the same as the untagged VLAN ID.
		for _, vlanID := range util.SplitNTrimSpace(value, ",", -1, true) {
			if vlanID == d.config["vlan"] {
				return fmt.Errorf("Tagged VLAN ID %q cannot be the same as untagged VLAN ID", vlanID)
			}

			_, _, err := validate.ParseNetworkVLANRange(vlanID)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Add bridge specific ipv4/ipv6 validation rules
	rules["ipv4.address"] = func(value string) error {
		if value == "" || value == "none" {
			return nil
		}

		return validate.IsNetworkAddressV4(value)
	}

	rules["ipv6.address"] = func(value string) error {
		if value == "" || value == "none" {
			return nil
		}

		return validate.IsNetworkAddressV6(value)
	}

	// Now run normal validation.
	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// checkAddressConflict checks for conflicting IP/MAC addresses on another NIC connected to same network on the
// same cluster member. Can only validate this when the instance is supplied (and not doing profile validation).
// Returns api.StatusError with status code set to http.StatusConflict if conflicting address found.
func (d *nicBridged) checkAddressConflict() error {
	node := d.inst.Location()

	ourNICIPs := make(map[string]net.IP, 2)
	ourNICIPs["ipv4.address"] = net.ParseIP(d.config["ipv4.address"])
	ourNICIPs["ipv6.address"] = net.ParseIP(d.config["ipv6.address"])

	ourNICMAC, _ := net.ParseMAC(d.config["hwaddr"])
	if ourNICMAC == nil {
		ourNICMAC, _ = net.ParseMAC(d.volatileGet()["hwaddr"])
	}

	// Check if any instance devices use this network.
	// Managed bridge networks have a per-server DHCP daemon so perform a node level search.
	filter := cluster.InstanceFilter{Node: &node}

	// Set network name for comparison (needs to support connecting to unmanaged networks).
	networkName := d.config["parent"]
	if d.network != nil {
		networkName = d.network.Name()
	}

	// Bridge networks are always in the default project.
	return network.UsedByInstanceDevices(d.state, api.ProjectDefaultName, networkName, "bridge", func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error {
		// Skip our own device. This avoids triggering duplicate device errors during
		// updates or when making temporary copies of our instance during migrations.
		sameLogicalInstance := instance.IsSameLogicalInstance(d.inst, &inst)
		if sameLogicalInstance && d.Name() == nicName {
			return nil
		}

		// Skip NICs connected to other VLANs (not perfect though as one NIC could
		// explicitly specify the default untagged VLAN and these would be connected to
		// same L2 even though the values are different, and there is a different default
		// value for native and openvswith parent bridges).
		if d.config["vlan"] != nicConfig["vlan"] {
			return nil
		}

		// Check there isn't another instance with the same DNS name connected to a managed network
		// that has DNS enabled and is connected to the same untagged VLAN.
		if d.network != nil && d.network.Config()["dns.mode"] != "none" && nicCheckDNSNameConflict(d.inst.Name(), inst.Name) {
			if sameLogicalInstance {
				return api.StatusErrorf(http.StatusConflict, "Instance DNS name %q conflict between %q and %q because both are connected to same network", strings.ToLower(inst.Name), d.name, nicName)
			}

			return api.StatusErrorf(http.StatusConflict, "Instance DNS name %q already used on network", strings.ToLower(inst.Name))
		}

		// Check NIC's MAC address doesn't match this NIC's MAC address.
		devNICMAC, _ := net.ParseMAC(nicConfig["hwaddr"])
		if devNICMAC == nil {
			devNICMAC, _ = net.ParseMAC(inst.Config[fmt.Sprintf("volatile.%s.hwaddr", nicName)])
		}

		if ourNICMAC != nil && devNICMAC != nil && bytes.Equal(ourNICMAC, devNICMAC) {
			return api.StatusErrorf(http.StatusConflict, "MAC address %q already defined on another NIC", devNICMAC.String())
		}

		// Check NIC's static IPs don't match this NIC's static IPs.
		for _, key := range []string{"ipv4.address", "ipv6.address"} {
			if d.config[key] == "" {
				continue // No static IP specified on this NIC.
			}

			// Parse IPs to avoid being tripped up by presentation differences.
			devNICIP := net.ParseIP(nicConfig[key])

			if ourNICIPs[key] != nil && devNICIP != nil && ourNICIPs[key].Equal(devNICIP) {
				return api.StatusErrorf(http.StatusConflict, "IP address %q already defined on another NIC", devNICIP.String())
			}
		}

		return nil
	}, filter)
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicBridged) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
	}

	return nil
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicBridged) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicBridged)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.priority", "ipv4.routes", "ipv6.routes", "ipv4.routes.external", "ipv6.routes.external", "ipv4.address", "ipv6.address", "security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"}
}

// Add is run when a device is added to a non-snapshot instance whether or not the instance is running.
func (d *nicBridged) Add() error {
	networkVethFillFromVolatile(d.config, d.volatileGet())

	// Rebuild dnsmasq entry if needed and reload.
	err := d.rebuildDnsmasqEntry()
	if err != nil {
		return err
	}

	return nil
}

// PreStartCheck checks the managed parent network is available (if relevant).
func (d *nicBridged) PreStartCheck() error {
	// Non-managed network NICs are not relevant for checking managed network availability.
	if d.network == nil {
		return nil
	}

	// If managed network is not available, don't try and start instance.
	if d.network.LocalStatus() == api.NetworkStatusUnavailable {
		return api.StatusErrorf(http.StatusServiceUnavailable, "Network %q unavailable on this server", d.network.Name())
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicBridged) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	var peerName string
	var mtu uint32

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	if d.inst.Type() == instancetype.Container {
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("veth", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}
		peerName, mtu, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("tap", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}
		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		mtu, err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Rebuild dnsmasq config if parent is a managed bridge network using dnsmasq and static lease file is
	// missing.
	bridgeNet, ok := d.network.(bridgeNetwork)
	if ok && d.network.IsManaged() && bridgeNet.UsesDNSMasq() {
		deviceStaticFileName := dnsmasq.DHCPStaticAllocationPath(d.network.Name(), dnsmasq.StaticAllocationFileName(d.inst.Project().Name, d.inst.Name(), d.Name()))
		if !util.PathExists(deviceStaticFileName) {
			err = d.rebuildDnsmasqEntry()
			if err != nil {
				return nil, fmt.Errorf("Failed creating DHCP static allocation: %w", err)
			}
		}
	}

	// Apply host-side routes to bridge interface.
	routes := []string{}
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes.external"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes.external"], ",", -1, true)...)
	err = networkNICRouteAdd(d.config["parent"], routes...)
	if err != nil {
		return nil, err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, nil, true)
	if err != nil {
		return nil, err
	}

	// Disable IPv6 on host-side veth interface (prevents host-side interface getting link-local address)
	// which isn't needed because the host-side interface is connected to a bridge.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", saveData["host_name"]), "1")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Apply and host-side network filters (uses enriched host_name from networkVethFillFromVolatile).
	r, err := d.setupHostFilters(nil)
	if err != nil {
		return nil, err
	}

	revert.Add(r)

	// Attach host side veth interface to bridge.
	err = network.AttachInterface(d.config["parent"], saveData["host_name"])
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = network.DetachInterface(d.config["parent"], saveData["host_name"]) })

	// Attempt to disable router advertisement acceptance.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Attempt to enable port isolation.
	if util.IsTrue(d.config["security.port_isolation"]) {
		link := &ip.Link{Name: saveData["host_name"]}
		err = link.BridgeLinkSetIsolated(true)
		if err != nil {
			return nil, err
		}
	}

	// Detect bridge type.
	nativeBridge := network.IsNativeBridge(d.config["parent"])

	// Setup VLAN settings on bridge port.
	if nativeBridge {
		err = d.setupNativeBridgePortVLANs(saveData["host_name"])
	} else {
		err = d.setupOVSBridgePortVLANs(saveData["host_name"])
	}

	if err != nil {
		return nil, err
	}

	// Check if hairpin mode needs to be enabled.
	if nativeBridge && d.network != nil {
		brNetfilterEnabled := false
		for _, ipVersion := range []uint{4, 6} {
			if network.BridgeNetfilterEnabled(ipVersion) == nil {
				brNetfilterEnabled = true
				break
			}
		}

		if brNetfilterEnabled {
			var listenAddresses map[int64]string

			err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				listenAddresses, err = tx.GetNetworkForwardListenAddresses(ctx, d.network.ID(), true)

				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed loading network forwards: %w", err)
			}

			// If br_netfilter is enabled and bridge has forwards, we enable hairpin mode on NIC's
			// bridge port in case any of the forwards target this NIC and the instance attempts to
			// connect to the forward's listener. Without hairpin mode on the target of the forward
			// will not be able to connect to the listener.
			if len(listenAddresses) > 0 {
				link := &ip.Link{Name: saveData["host_name"]}
				err = link.BridgeLinkSetHairpin(true)
				if err != nil {
					return nil, fmt.Errorf("Error enabling hairpin mode on bridge port %q: %w", link.Name, err)
				}

				d.logger.Debug("Enabled hairpin mode on NIC bridge port", logger.Ctx{"dev": link.Name})
			}
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.postStart}

	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// postStart is run after the device is added to the instance.
func (d *nicBridged) postStart() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *nicBridged) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]
	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)
	networkVethFillFromVolatile(oldConfig, v)

	// If an IPv6 address has changed, flush all existing IPv6 leases for instance so instance
	// isn't allocated old IP. This is important with IPv6 because DHCPv6 supports multiple IP
	// address allocation and would result in instance having leases for both old and new IPs.
	if d.config["hwaddr"] != "" && d.config["ipv6.address"] != oldConfig["ipv6.address"] {
		err := d.networkClearLease(d.inst.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseIPv6Only)
		if err != nil {
			return err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// If instance is running, apply host side limits and filters first before rebuilding
	// dnsmasq config below so that existing config can be used as part of the filter removal.
	if isRunning {
		err := d.validateEnvironment()
		if err != nil {
			return err
		}

		// Validate old config so that it is enriched with network parent config needed for route removal.
		err = Validate(d.inst, d.state, d.name, oldConfig)
		if err != nil {
			return err
		}

		// Remove old host-side routes from bridge interface.

		oldRoutes := []string{}
		oldRoutes = append(oldRoutes, util.SplitNTrimSpace(oldConfig["ipv4.routes"], ",", -1, true)...)
		oldRoutes = append(oldRoutes, util.SplitNTrimSpace(oldConfig["ipv6.routes"], ",", -1, true)...)
		oldRoutes = append(oldRoutes, util.SplitNTrimSpace(oldConfig["ipv4.routes.external"], ",", -1, true)...)
		oldRoutes = append(oldRoutes, util.SplitNTrimSpace(oldConfig["ipv6.routes.external"], ",", -1, true)...)
		networkNICRouteDelete(oldConfig["parent"], oldRoutes...)

		// Apply host-side routes to bridge interface.
		routes := []string{}
		routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true)...)
		routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)
		routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes.external"], ",", -1, true)...)
		routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes.external"], ",", -1, true)...)
		err = networkNICRouteAdd(d.config["parent"], routes...)
		if err != nil {
			return err
		}

		// Apply host-side limits.
		err = networkSetupHostVethLimits(&d.deviceCommon, oldConfig, true)
		if err != nil {
			return err
		}

		// Apply and host-side network filters (uses enriched host_name from networkVethFillFromVolatile).
		r, err := d.setupHostFilters(oldConfig)
		if err != nil {
			return err
		}

		revert.Add(r)
	}

	// Rebuild dnsmasq entry if needed and reload.
	err := d.rebuildDnsmasqEntry()
	if err != nil {
		return err
	}

	// If an IPv6 address has changed, if the instance is running we should bounce the host-side
	// veth interface to give the instance a chance to detect the change and re-apply for an
	// updated lease with new IP address.
	if d.config["ipv6.address"] != oldConfig["ipv6.address"] && d.config["host_name"] != "" && util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		link := &ip.Link{Name: d.config["host_name"]}
		err := link.SetDown()
		if err != nil {
			return err
		}

		err = link.SetUp()
		if err != nil {
			return err
		}
	}

	// If an external address changed, update the BGP advertisements.
	err = bgpRemovePrefix(&d.deviceCommon, oldConfig)
	if err != nil {
		return err
	}

	err = bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicBridged) Stop() (*deviceConfig.RunConfig, error) {
	// Remove BGP announcements.
	err := bgpRemovePrefix(&d.deviceCommon, d.config)
	if err != nil {
		return nil, err
	}

	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	err = networkClearHostVethLimits(&d.deviceCommon)
	if err != nil {
		return nil, err
	}

	// Setup post-stop actions.
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicBridged) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name": "",
		})
	}()

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
		// Detach host-side end of veth pair from bridge (required for openvswitch particularly).
		err := network.DetachInterface(d.config["parent"], d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to detach interface %q from %q: %w", d.config["host_name"], d.config["parent"], err)
		}

		// Removing host-side end of veth pair will delete the peer end too.
		err = network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %q: %w", d.config["host_name"], err)
		}
	}

	// Remove host-side routes from bridge interface.
	routes := []string{}
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv4.routes.external"], ",", -1, true)...)
	routes = append(routes, util.SplitNTrimSpace(d.config["ipv6.routes.external"], ",", -1, true)...)
	networkNICRouteDelete(d.config["parent"], routes...)

	if util.IsTrue(d.config["security.mac_filtering"]) || util.IsTrue(d.config["security.ipv4_filtering"]) || util.IsTrue(d.config["security.ipv6_filtering"]) {
		d.removeFilters(d.config)
	}

	return nil
}

// Remove is run when the device is removed from the instance or the instance is deleted.
func (d *nicBridged) Remove() error {
	if d.config["parent"] != "" {
		dnsmasq.ConfigMutex.Lock()
		defer dnsmasq.ConfigMutex.Unlock()

		if network.InterfaceExists(d.config["parent"]) {
			err := d.networkClearLease(d.inst.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseAll)
			if err != nil {
				return fmt.Errorf("Failed clearing leases: %w", err)
			}
		}

		// Remove dnsmasq config if it exists (doesn't return error if file is missing).
		err := dnsmasq.RemoveStaticEntry(d.config["parent"], d.inst.Project().Name, d.inst.Name(), d.Name())
		if err != nil {
			return err
		}

		// Reload dnsmasq to apply new settings if dnsmasq is running.
		err = dnsmasq.Kill(d.config["parent"], true)
		if err != nil {
			return err
		}
	}

	return nil
}

// rebuildDnsmasqEntry rebuilds the dnsmasq host entry if connected to a managed network and reloads dnsmasq.
func (d *nicBridged) rebuildDnsmasqEntry() error {
	// Rebuild dnsmasq config if parent is a managed bridge network using dnsmasq.
	bridgeNet, ok := d.network.(bridgeNetwork)
	if !ok || !d.network.IsManaged() || !bridgeNet.UsesDNSMasq() {
		return nil
	}

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	ipv4Address := d.config["ipv4.address"]
	ipv6Address := d.config["ipv6.address"]

	// If address is set to none treat it the same as not being specified
	if ipv4Address == "none" {
		ipv4Address = ""
	}

	if ipv6Address == "none" {
		ipv6Address = ""
	}

	// If IP filtering is enabled, and no static IP in config, check if there is already a
	// dynamically assigned static IP in dnsmasq config and write that back out in new config.
	if (util.IsTrue(d.config["security.ipv4_filtering"]) && ipv4Address == "") || (util.IsTrue(d.config["security.ipv6_filtering"]) && ipv6Address == "") {
		deviceStaticFileName := dnsmasq.StaticAllocationFileName(d.inst.Project().Name, d.inst.Name(), d.Name())
		_, curIPv4, curIPv6, err := dnsmasq.DHCPStaticAllocation(d.config["parent"], deviceStaticFileName)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		if ipv4Address == "" && curIPv4.IP != nil {
			ipv4Address = curIPv4.IP.String()
		}

		if ipv6Address == "" && curIPv6.IP != nil {
			ipv6Address = curIPv6.IP.String()
		}
	}

	err := dnsmasq.UpdateStaticEntry(d.config["parent"], d.inst.Project().Name, d.inst.Name(), d.Name(), d.network.Config(), d.config["hwaddr"], ipv4Address, ipv6Address)
	if err != nil {
		return err
	}

	// Reload dnsmasq to apply new settings.
	err = dnsmasq.Kill(d.config["parent"], true)
	if err != nil {
		return err
	}

	return nil
}

// setupHostFilters applies any host side network filters.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func (d *nicBridged) setupHostFilters(oldConfig deviceConfig.Device) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Check br_netfilter kernel module is loaded and enabled for IPv6 before clearing existing rules.
	// We won't try to load it as its default mode can cause unwanted traffic blocking.
	if util.IsTrue(d.config["security.ipv6_filtering"]) {
		err := network.BridgeNetfilterEnabled(6)
		if err != nil {
			return nil, fmt.Errorf("security.ipv6_filtering requires bridge netfilter: %w", err)
		}
	}

	// Remove any old network filters if non-empty oldConfig supplied as part of update.
	if oldConfig != nil && (util.IsTrue(oldConfig["security.mac_filtering"]) || util.IsTrue(oldConfig["security.ipv4_filtering"]) || util.IsTrue(oldConfig["security.ipv6_filtering"])) {
		d.removeFilters(oldConfig)
	}

	// Setup network filters.
	if util.IsTrue(d.config["security.mac_filtering"]) || util.IsTrue(d.config["security.ipv4_filtering"]) || util.IsTrue(d.config["security.ipv6_filtering"]) {
		err := d.setFilters()
		if err != nil {
			return nil, err
		}

		revert.Add(func() { d.removeFilters(d.config) })
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// removeFilters removes any network level filters defined for the instance.
func (d *nicBridged) removeFilters(m deviceConfig.Device) {
	if m["hwaddr"] == "" {
		d.logger.Error("Failed to remove network filters: hwaddr not defined")
		return
	}

	if m["host_name"] == "" {
		d.logger.Error("Failed to remove network filters: host_name not defined")
		return
	}

	IPv4Nets, IPv6Nets, err := allowedIPNets(m)
	if err != nil {
		d.logger.Error("Failed to calculate static IP network filters", logger.Ctx{"err": err})
		return
	}

	// Remove filters for static MAC and IPs (if specified above).
	// This covers the case when filtering is used with an unmanaged bridge.
	d.logger.Debug("Clearing instance firewall static filters", logger.Ctx{"parent": m["parent"], "host_name": m["host_name"], "hwaddr": m["hwaddr"], "IPv4Nets": IPv4Nets, "IPv6Nets": IPv6Nets})
	err = d.state.Firewall.InstanceClearBridgeFilter(d.inst.Project().Name, d.inst.Name(), d.name, m["parent"], m["host_name"], m["hwaddr"], IPv4Nets, IPv6Nets)
	if err != nil {
		d.logger.Error("Failed to remove static IP network filters", logger.Ctx{"err": err})
	}

	// If allowedIPNets returned nil for IPv4 or IPv6, it is possible that total protocol blocking was set up
	// because the device has a managed parent network with DHCP disabled. Pass in empty slices to catch this case.
	d.logger.Debug("Clearing instance total protocol filters", logger.Ctx{"parent": m["parent"], "host_name": m["host_name"], "hwaddr": m["hwaddr"], "IPv4Nets": IPv4Nets, "IPv6Nets": IPv6Nets})
	err = d.state.Firewall.InstanceClearBridgeFilter(d.inst.Project().Name, d.inst.Name(), d.name, m["parent"], m["host_name"], m["hwaddr"], make([]*net.IPNet, 0), make([]*net.IPNet, 0))
	if err != nil {
		d.logger.Error("Failed to remove total protocol network filters", logger.Ctx{"err": err})
	}

	// Read current static DHCP IP allocation configured from dnsmasq host config (if exists).
	// This covers the case when IPs are not defined in config, but have been assigned in managed DHCP.
	deviceStaticFileName := dnsmasq.StaticAllocationFileName(d.inst.Project().Name, d.inst.Name(), d.Name())
	_, IPv4Alloc, IPv6Alloc, err := dnsmasq.DHCPStaticAllocation(m["parent"], deviceStaticFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}

		d.logger.Error("Failed to get static IP allocations for filter removal", logger.Ctx{"err": err})
		return
	}

	// We have already cleared any "ipv{n}.routes" etc. above, so we just need to clear the DHCP allocated IPs.
	var IPv4AllocNets []*net.IPNet
	if len(IPv4Alloc.IP) > 0 {
		_, IPv4AllocNet, err := net.ParseCIDR(fmt.Sprintf("%s/32", IPv4Alloc.IP.String()))
		if err != nil {
			d.logger.Error("Failed to generate subnet from dynamically generated IPv4 address", logger.Ctx{"err": err})
		} else {
			IPv4AllocNets = append(IPv4AllocNets, IPv4AllocNet)
		}
	}

	var IPv6AllocNets []*net.IPNet
	if len(IPv6Alloc.IP) > 0 {
		_, IPv6AllocNet, err := net.ParseCIDR(fmt.Sprintf("%s/128", IPv6Alloc.IP.String()))
		if err != nil {
			d.logger.Error("Failed to generate subnet from dynamically generated IPv6Address", logger.Ctx{"err": err})
		} else {
			IPv6AllocNets = append(IPv6AllocNets, IPv6AllocNet)
		}
	}

	d.logger.Debug("Clearing instance firewall dynamic filters", logger.Ctx{"parent": m["parent"], "host_name": m["host_name"], "hwaddr": m["hwaddr"], "ipv4": IPv4Alloc.IP, "ipv6": IPv6Alloc.IP})
	err = d.state.Firewall.InstanceClearBridgeFilter(d.inst.Project().Name, d.inst.Name(), d.name, m["parent"], m["host_name"], m["hwaddr"], IPv4AllocNets, IPv6AllocNets)
	if err != nil {
		logger.Errorf("Failed to remove DHCP network assigned filters  for %q: %v", d.name, err)
	}
}

// setFilters sets up any network level filters defined for the instance.
// These are controlled by the security.mac_filtering, security.ipv4_Filtering and security.ipv6_filtering config keys.
func (d *nicBridged) setFilters() (err error) {
	if d.config["hwaddr"] == "" {
		return fmt.Errorf("Failed to set network filters: require hwaddr defined")
	}

	if d.config["host_name"] == "" {
		return fmt.Errorf("Failed to set network filters: require host_name defined")
	}

	if d.config["parent"] == "" {
		return fmt.Errorf("Failed to set network filters: require parent defined")
	}

	// Parse device config.
	mac, err := net.ParseMAC(d.config["hwaddr"])
	if err != nil {
		return fmt.Errorf("Invalid hwaddr: %w", err)
	}

	// Parse static IPs, relies on invalid IPs being set to nil.
	IPv4 := net.ParseIP(d.config["ipv4.address"])
	IPv6 := net.ParseIP(d.config["ipv6.address"])

	// If parent bridge is unmanaged check that a manually specified IP is available if IP filtering enabled.
	if d.network == nil {
		if util.IsTrue(d.config["security.ipv4_filtering"]) && d.config["ipv4.address"] == "" {
			return fmt.Errorf("IPv4 filtering requires a manually specified ipv4.address when using an unmanaged parent bridge")
		}

		if util.IsTrue(d.config["security.ipv6_filtering"]) && d.config["ipv6.address"] == "" {
			return fmt.Errorf("IPv6 filtering requires a manually specified ipv6.address when using an unmanaged parent bridge")
		}
	}

	// Use a clone of the config. This can be amended with the allocated IPs so that the correct ones are added to the firewall.
	config := d.config.Clone()

	// If parent bridge is managed, allocate the static IPs (if needed).
	if d.network != nil && (IPv4 == nil || IPv6 == nil) {
		opts := &dhcpalloc.Options{
			ProjectName: d.inst.Project().Name,
			HostName:    d.inst.Name(),
			DeviceName:  d.Name(),
			HostMAC:     mac,
			Network:     d.network,
		}

		err = dhcpalloc.AllocateTask(opts, func(t *dhcpalloc.Transaction) error {
			if util.IsTrue(config["security.ipv4_filtering"]) && IPv4 == nil && config["ipv4.address"] != "none" {
				IPv4, err = t.AllocateIPv4()
				config["ipv4.address"] = IPv4.String()

				// If DHCP not supported, skip error and set the address to "none", and will result in total protocol filter.
				if err == dhcpalloc.ErrDHCPNotSupported {
					config["ipv4.address"] = "none"
				} else if err != nil {
					return err
				}
			}

			if util.IsTrue(config["security.ipv6_filtering"]) && IPv6 == nil && config["ipv6.address"] != "none" {
				IPv6, err = t.AllocateIPv6()
				config["ipv6.address"] = IPv6.String()

				// If DHCP not supported, skip error and set the address to "none", and will result in total protocol filter.
				if err == dhcpalloc.ErrDHCPNotSupported {
					config["ipv6.address"] = "none"
				} else if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil && err != dhcpalloc.ErrDHCPNotSupported {
			return err
		}
	}

	// If anything goes wrong, clean up so we don't leave orphaned rules.
	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() { d.removeFilters(config) })

	IPv4Nets, IPv6Nets, err := allowedIPNets(config)
	if err != nil {
		return err
	}

	err = d.state.Firewall.InstanceSetupBridgeFilter(d.inst.Project().Name, d.inst.Name(), d.name, d.config["parent"], d.config["host_name"], d.config["hwaddr"], IPv4Nets, IPv6Nets, d.network != nil)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// allowedIPNets accepts a device config. For each IP version it returns nil if all addresses should be allowed,
// an empty slice if all addresses should be blocked, and a populated slice of subnets to allow traffic from specific ranges.
func allowedIPNets(config deviceConfig.Device) (IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet, err error) {
	getAllowedNets := func(ipVersion int) ([]*net.IPNet, error) {
		if util.IsFalseOrEmpty(config[fmt.Sprintf("security.ipv%d_filtering", ipVersion)]) {
			// Return nil (allow all)
			return nil, nil
		}

		ipAddr := config[fmt.Sprintf("ipv%d.address", ipVersion)]
		if ipAddr == "none" {
			// Return an empty slice to block all traffic.
			return []*net.IPNet{}, nil
		}

		var routes []string

		// Get a CIDR string for the instance address
		if ipAddr != "" {
			if ipVersion == 4 {
				routes = append(routes, fmt.Sprintf("%s/32", ipAddr))
			} else if ipVersion == 6 {
				routes = append(routes, fmt.Sprintf("%s/128", ipAddr))
			}
		}

		// Get remaining allowed routes from config.
		routes = append(routes, util.SplitNTrimSpace(config[fmt.Sprintf("ipv%d.routes", ipVersion)], ",", -1, true)...)
		routes = append(routes, util.SplitNTrimSpace(config[fmt.Sprintf("ipv%d.routes.external", ipVersion)], ",", -1, true)...)

		var allowedNets []*net.IPNet
		for _, route := range routes {
			ipNet, err := network.ParseIPCIDRToNet(route)
			if err != nil {
				return nil, err
			}

			allowedNets = append(allowedNets, ipNet)
		}

		return allowedNets, nil
	}

	IPv4Nets, err = getAllowedNets(4)
	if err != nil {
		return nil, nil, err
	}

	IPv6Nets, err = getAllowedNets(6)
	if err != nil {
		return nil, nil, err
	}

	return IPv4Nets, IPv6Nets, nil
}

const (
	clearLeaseAll = iota
	clearLeaseIPv4Only
	clearLeaseIPv6Only
)

// networkClearLease clears leases from a running dnsmasq process.
func (d *nicBridged) networkClearLease(name string, network string, hwaddr string, mode int) error {
	leaseFile := internalUtil.VarPath("networks", network, "dnsmasq.leases")

	// Check that we are in fact running a dnsmasq for the network
	if !util.PathExists(leaseFile) {
		return nil
	}

	// Convert MAC string to bytes to avoid any case comparison issues later.
	srcMAC, err := net.ParseMAC(hwaddr)
	if err != nil {
		return err
	}

	iface, err := net.InterfaceByName(network)
	if err != nil {
		return fmt.Errorf("Failed getting bridge interface state for %q: %w", network, err)
	}

	// Get IPv4 and IPv6 address of interface running dnsmasq on host.
	addrs, err := iface.Addrs()
	if err != nil {
		return fmt.Errorf("Failed getting bridge interface addresses for %q: %w", network, err)
	}

	var dstIPv4, dstIPv6 net.IP
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			return err
		}

		if !ip.IsGlobalUnicast() {
			continue
		}

		if ip.To4() == nil {
			dstIPv6 = ip
		} else {
			dstIPv4 = ip
		}
	}

	// Iterate the dnsmasq leases file looking for matching leases for this instance to release.
	file, err := os.Open(leaseFile)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	var dstDUID string
	errs := []error{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		fieldsLen := len(fields)

		// Handle lease lines
		if fieldsLen == 5 {
			if (mode == clearLeaseAll || mode == clearLeaseIPv4Only) && srcMAC.String() == fields[1] { // Handle IPv4 leases by matching MAC address to lease.
				srcIP := net.ParseIP(fields[2])

				if dstIPv4 == nil {
					logger.Warnf("Failed to release DHCPv4 lease for instance %q, IP %q, MAC %q, %v", name, srcIP, srcMAC, "No server address found")
					continue // Cant send release packet if no dstIP found.
				}

				err = d.networkDHCPv4Release(srcMAC, srcIP, dstIPv4)
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv4 lease for instance %q, IP %q, MAC %q, %v", name, srcIP, srcMAC, err))
				}
			} else if (mode == clearLeaseAll || mode == clearLeaseIPv6Only) && name == fields[3] { // Handle IPv6 addresses by matching hostname to lease.
				IAID := fields[1]
				srcIP := net.ParseIP(fields[2])
				DUID := fields[4]

				// Skip IPv4 addresses.
				if srcIP.To4() != nil {
					continue
				}

				if dstIPv6 == nil {
					logger.Warnf("Failed to release DHCPv6 lease for instance %q, IP %q, DUID %q, IAID %q: %q", name, srcIP, DUID, IAID, "No server address found")
					continue // Cant send release packet if no dstIP found.
				}

				if dstDUID == "" {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv6 lease for instance %q, IP %q, DUID %q, IAID %q: %s", name, srcIP, DUID, IAID, "No server DUID found"))
					continue // Cant send release packet if no dstDUID found.
				}

				err = d.networkDHCPv6Release(DUID, IAID, srcIP, dstIPv6, dstDUID)
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv6 lease for instance %q, IP %q, DUID %q, IAID %q: %w", name, srcIP, DUID, IAID, err))
				}
			}
		} else if fieldsLen == 2 && fields[0] == "duid" {
			// Handle server DUID line needed for releasing IPv6 leases.
			// This should come before the IPv6 leases in the lease file.
			dstDUID = fields[1]
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	err = scanner.Err()
	if err != nil {
		return err
	}

	return nil
}

// networkDHCPv4Release sends a DHCPv4 release packet to a DHCP server.
func (d *nicBridged) networkDHCPv4Release(srcMAC net.HardwareAddr, srcIP net.IP, dstIP net.IP) error {
	dstAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:67", dstIP.String()))
	if err != nil {
		return err
	}

	conn, err := net.DialUDP("udp", nil, dstAddr)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	//Random DHCP transaction ID
	xid := rand.Uint32()

	// Construct a DHCP packet pretending to be from the source IP and MAC supplied.
	dhcp := layers.DHCPv4{
		Operation:    layers.DHCPOpRequest,
		HardwareType: layers.LinkTypeEthernet,
		ClientHWAddr: srcMAC,
		ClientIP:     srcIP,
		Xid:          xid,
	}

	// Add options to DHCP release packet.
	dhcp.Options = append(dhcp.Options,
		layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeRelease)}),
		layers.NewDHCPOption(layers.DHCPOptServerID, dstIP.To4()),
	)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}

	err = gopacket.SerializeLayers(buf, opts, &dhcp)
	if err != nil {
		return err
	}

	_, err = conn.Write(buf.Bytes())
	if err != nil {
		return err
	}

	return conn.Close()
}

// networkDHCPv6Release sends a DHCPv6 release packet to a DHCP server.
func (d *nicBridged) networkDHCPv6Release(srcDUID string, srcIAID string, srcIP net.IP, dstIP net.IP, dstDUID string) error {
	dstAddr, err := net.ResolveUDPAddr("udp6", fmt.Sprintf("[%s]:547", dstIP.String()))
	if err != nil {
		return err
	}

	conn, err := net.DialUDP("udp6", nil, dstAddr)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	// Construct a DHCPv6 packet pretending to be from the source IP and MAC supplied.
	dhcp := layers.DHCPv6{
		MsgType: layers.DHCPv6MsgTypeRelease,
	}

	// Convert Server DUID from string to byte array
	dstDUIDRaw, err := hex.DecodeString(strings.Replace(dstDUID, ":", "", -1))
	if err != nil {
		return err
	}

	// Convert DUID from string to byte array
	srcDUIDRaw, err := hex.DecodeString(strings.Replace(srcDUID, ":", "", -1))
	if err != nil {
		return err
	}

	// Convert IAID string to int
	srcIAIDRaw, err := strconv.ParseUint(srcIAID, 10, 32)
	if err != nil {
		return err
	}

	srcIAIDRaw32 := uint32(srcIAIDRaw)

	// Build the Identity Association details option manually (as not provided by gopacket).
	iaAddr := d.networkDHCPv6CreateIAAddress(srcIP)
	ianaRaw := d.networkDHCPv6CreateIANA(srcIAIDRaw32, iaAddr)

	// Add options to DHCP release packet.
	dhcp.Options = append(dhcp.Options,
		layers.NewDHCPv6Option(layers.DHCPv6OptServerID, dstDUIDRaw),
		layers.NewDHCPv6Option(layers.DHCPv6OptClientID, srcDUIDRaw),
		layers.NewDHCPv6Option(layers.DHCPv6OptIANA, ianaRaw),
	)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}

	err = gopacket.SerializeLayers(buf, opts, &dhcp)
	if err != nil {
		return err
	}

	_, err = conn.Write(buf.Bytes())
	if err != nil {
		return err
	}

	return conn.Close()
}

// networkDHCPv6CreateIANA creates a DHCPv6 Identity Association for Non-temporary Address (rfc3315 IA_NA) option.
func (d *nicBridged) networkDHCPv6CreateIANA(IAID uint32, IAAddr []byte) []byte {
	data := make([]byte, 12)
	binary.BigEndian.PutUint32(data[0:4], IAID)       // Identity Association Identifier
	binary.BigEndian.PutUint32(data[4:8], uint32(0))  // T1
	binary.BigEndian.PutUint32(data[8:12], uint32(0)) // T2
	data = append(data, IAAddr...)                    // Append the IA Address details
	return data
}

// networkDHCPv6CreateIAAddress creates a DHCPv6 Identity Association Address (rfc3315) option.
func (d *nicBridged) networkDHCPv6CreateIAAddress(IP net.IP) []byte {
	data := make([]byte, 28)
	binary.BigEndian.PutUint16(data[0:2], uint16(layers.DHCPv6OptIAAddr)) // Sub-Option type
	binary.BigEndian.PutUint16(data[2:4], uint16(24))                     // Length (fixed at 24 bytes)
	copy(data[4:20], IP)                                                  // IPv6 address to be released
	binary.BigEndian.PutUint32(data[20:24], uint32(0))                    // Preferred liftetime
	binary.BigEndian.PutUint32(data[24:28], uint32(0))                    // Valid lifetime
	return data
}

// setupNativeBridgePortVLANs configures the bridge port with the specified VLAN settings on the native bridge.
func (d *nicBridged) setupNativeBridgePortVLANs(hostName string) error {
	link := &ip.Link{Name: hostName}

	// Check vlan_filtering is enabled on bridge if needed.
	if d.config["vlan"] != "" || d.config["vlan.tagged"] != "" {
		vlanFilteringStatus, err := network.BridgeVLANFilteringStatus(d.config["parent"])
		if err != nil {
			return err
		}

		if vlanFilteringStatus != "1" {
			return fmt.Errorf("VLAN filtering is not enabled in parent bridge %q", d.config["parent"])
		}
	}

	// Set port on bridge to specified untagged PVID.
	if d.config["vlan"] != "" {
		// Reject VLAN ID 0 if specified (as validation allows VLAN ID 0 on unmanaged bridges for OVS).
		if d.config["vlan"] == "0" {
			return fmt.Errorf("VLAN ID 0 is not allowed for native Linux bridges")
		}

		// Get default PVID membership on port.
		defaultPVID, err := network.BridgeVLANDefaultPVID(d.config["parent"])
		if err != nil {
			return err
		}

		// If the bridge has a default PVID and it is different to the specified untagged VLAN or if tagged
		// VLAN is set to "none" then remove the default untagged membership.
		if defaultPVID != "0" && (defaultPVID != d.config["vlan"] || d.config["vlan"] == "none") {
			err = link.BridgeVLANDelete(defaultPVID, false)
			if err != nil {
				return fmt.Errorf("Failed removing default PVID membership: %w", err)
			}
		}

		// Configure the untagged membership settings of the port if VLAN ID specified.
		if d.config["vlan"] != "none" {
			err = link.BridgeVLANAdd(d.config["vlan"], true, true, false)
			if err != nil {
				return err
			}
		}
	}

	// Add any tagged VLAN memberships.
	if d.config["vlan.tagged"] != "" {
		networkVLANList, err := networkVLANListExpand(util.SplitNTrimSpace(d.config["vlan.tagged"], ",", -1, true))
		if err != nil {
			return err
		}

		for _, vlanID := range networkVLANList {
			// Reject VLAN ID 0 if specified (as validation allows VLAN ID 0 on unmanaged bridges for OVS).
			if vlanID == 0 {
				return fmt.Errorf("VLAN tagged ID 0 is not allowed for native Linux bridges")
			}

			err := link.BridgeVLANAdd(fmt.Sprintf("%d", vlanID), false, false, false)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// setupOVSBridgePortVLANs configures the bridge port with the specified VLAN settings on the openvswitch bridge.
func (d *nicBridged) setupOVSBridgePortVLANs(hostName string) error {
	vswitch, err := ovs.NewVSwitch()
	if err != nil {
		return fmt.Errorf("Failed to connect to OVS: %w", err)
	}

	// Set port on bridge to specified untagged PVID.
	if d.config["vlan"] != "" {
		if d.config["vlan"] == "none" && d.config["vlan.tagged"] == "" {
			return fmt.Errorf("vlan=none is not supported with openvswitch bridges when not using vlan.tagged")
		}

		// Configure the untagged 'native' membership settings of the port if VLAN ID specified.
		// Also set the vlan_mode=access, which will drop any tagged frames.
		// Order is important here, as vlan_mode is set to "access", assuming that vlan.tagged is not used.
		// If vlan.tagged is specified, then we expect it to also change the vlan_mode as needed.
		if d.config["vlan"] != "none" {
			err := vswitch.BridgePortSet(hostName, "vlan_mode=access", fmt.Sprintf("tag=%s", d.config["vlan"]))
			if err != nil {
				return err
			}
		}
	}

	// Add any tagged VLAN memberships.
	if d.config["vlan.tagged"] != "" {
		intNetworkVLANs, err := networkVLANListExpand(util.SplitNTrimSpace(d.config["vlan.tagged"], ",", -1, true))
		if err != nil {
			return err
		}

		var vlanIDs []string

		for _, intNetworkVLAN := range intNetworkVLANs {
			vlanIDs = append(vlanIDs, strconv.Itoa(intNetworkVLAN))
		}

		vlanMode := "trunk" // Default to only allowing tagged frames (drop untagged frames).
		if d.config["vlan"] != "none" {
			// If untagged vlan mode isn't "none" then allow untagged frames for port's 'native' VLAN.
			vlanMode = "native-untagged"
		}

		// Configure the tagged membership settings of the port if VLAN ID specified.
		// Also set the vlan_mode as needed from above.
		// Must come after the PortSet command used for setting "vlan" mode above so that the correct
		// vlan_mode is retained.
		err = vswitch.BridgePortSet(hostName, fmt.Sprintf("vlan_mode=%s", vlanMode), fmt.Sprintf("trunks=%s", strings.Join(vlanIDs, ",")))
		if err != nil {
			return err
		}
	}

	return nil
}

// State gets the state of a bridged NIC by parsing the local DHCP server leases file.
func (d *nicBridged) State() (*api.InstanceStateNetwork, error) {
	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)

	ips := []net.IP{}
	var v4mask string
	var v6mask string

	// ipStore appends an IP to ips if not already stored.
	ipStore := func(newIP net.IP) {
		for _, ip := range ips {
			if ip.Equal(newIP) {
				return
			}
		}

		ips = append(ips, newIP)
	}

	hwAddr, _ := net.ParseMAC(d.config["hwaddr"])

	if d.network != nil {
		// Extract subnet sizes from bridge addresses if available.
		netConfig := d.network.Config()
		_, v4subnet, _ := net.ParseCIDR(netConfig["ipv4.address"])
		_, v6subnet, _ := net.ParseCIDR(netConfig["ipv6.address"])

		if v4subnet != nil {
			mask, _ := v4subnet.Mask.Size()
			v4mask = fmt.Sprintf("%d", mask)
		}

		if v6subnet != nil {
			mask, _ := v6subnet.Mask.Size()
			v6mask = fmt.Sprintf("%d", mask)
		}

		if d.config["hwaddr"] != "" {
			// Parse the leases file if parent network is managed.
			leaseIPs, err := network.GetLeaseAddresses(d.network.Name(), d.config["hwaddr"])
			if err == nil {
				for _, leaseIP := range leaseIPs {
					ipStore(leaseIP)
				}
			}

			if util.IsFalseOrEmpty(d.network.Config()["ipv6.dhcp.stateful"]) && v6subnet != nil {
				// If stateful DHCPv6 is disabled, and IPv6 is enabled on the bridge, the NIC
				// is likely to use its MAC and SLAAC to configure its address.
				if hwAddr != nil {
					ip, err := eui64.ParseMAC(v6subnet.IP, hwAddr)
					if err == nil {
						ipStore(ip)
					}
				}
			}
		}
	}

	// Get IP addresses from IP neighbour cache if present.
	neighIPs, err := network.GetNeighbourIPs(d.config["parent"], hwAddr)
	if err == nil {
		validStates := []string{
			string(ip.NeighbourIPStatePermanent),
			string(ip.NeighbourIPStateNoARP),
			string(ip.NeighbourIPStateReachable),
		}

		// Add any valid-state neighbour IP entries first.
		for _, neighIP := range neighIPs {
			if slices.Contains(validStates, string(neighIP.State)) {
				ipStore(neighIP.Addr)
			}
		}

		// Add any non-failed-state entries.
		for _, neighIP := range neighIPs {
			if neighIP.State != ip.NeighbourIPStateFailed && !slices.Contains(validStates, string(neighIP.State)) {
				ipStore(neighIP.Addr)
			}
		}
	}

	// Convert IPs to InstanceStateNetworkAddresses.
	addresses := []api.InstanceStateNetworkAddress{}
	for _, ip := range ips {
		addr := api.InstanceStateNetworkAddress{}
		addr.Address = ip.String()
		addr.Family = "inet"
		addr.Netmask = v4mask

		if ip.To4() == nil {
			addr.Family = "inet6"
			addr.Netmask = v6mask
		}

		if ip.IsLinkLocalUnicast() {
			addr.Scope = "link"

			if addr.Family == "inet6" {
				addr.Netmask = "64" // Link-local IPv6 addresses are /64.
			} else {
				addr.Netmask = "16" // Link-local IPv4 addresses are /16.
			}
		} else {
			addr.Scope = "global"
		}

		addresses = append(addresses, addr)
	}

	mtu, err := d.getHostMTU()
	if err != nil {
		d.logger.Warn("Failed getting host interface state for MTU", logger.Ctx{"host_name": d.config["host_name"], "err": err})
	}

	// Retrieve the host counters, as we report the values from the instance's point of view,
	// those counters need to be reversed below.
	hostCounters, err := resources.GetNetworkCounters(d.config["host_name"])
	if err != nil {
		return nil, fmt.Errorf("Failed getting network interface counters: %w", err)
	}

	network := api.InstanceStateNetwork{
		Addresses: addresses,
		Counters: api.InstanceStateNetworkCounters{
			BytesReceived:   hostCounters.BytesSent,
			BytesSent:       hostCounters.BytesReceived,
			PacketsReceived: hostCounters.PacketsSent,
			PacketsSent:     hostCounters.PacketsReceived,
		},
		Hwaddr:   d.config["hwaddr"],
		HostName: d.config["host_name"],
		Mtu:      mtu,
		State:    "up",
		Type:     "broadcast",
	}

	return &network, nil
}

func (d *nicBridged) getHostMTU() (int, error) {
	// Get MTU of host interface if exists.
	iface, err := net.InterfaceByName(d.config["host_name"])
	if err != nil {
		return 0, err
	}

	mtu := -1
	if iface != nil {
		mtu = iface.MTU
	}

	return mtu, nil
}

// Register sets up anything needed on startup.
func (d *nicBridged) Register() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}
