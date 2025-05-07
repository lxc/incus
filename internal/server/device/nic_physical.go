package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/dnsmasq"
	"github.com/lxc/incus/v6/internal/server/dnsmasq/dhcpalloc"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/network/acl"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/resources"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

type nicPhysical struct {
	deviceCommon

	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicPhysical) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}

	// NOTE: may need to add more fields due to bridge code
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_physical, key=parent)
		//
		// ---
		//  type: string
		//  managed: yes
		//  shortdesc: The name of the parent host device (required if specifying the `nictype` directly)
		"parent",

		// gendoc:generate(entity=devices, group=nic_physical, key=name)
		//
		// ---
		//  type: string
		//  default: kernel assigned
		//  managed: no
		//  shortdesc: The name of the interface inside the instance
		"name",

		// gendoc:generate(entity=devices, group=nic_physical, key=boot.priority)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: Boot priority for VMs (higher value boots first)
		"boot.priority",

		// gendoc:generate(entity=devices, group=nic_physical, key=gvrp)
		//
		// ---
		//  type: bool
		//  default: false
		//  managed: no
		//  shortdesc: Register VLAN using GARP VLAN Registration Protocol
		"gvrp",

		// gendoc:generate(entity=devices, group=nic_physical, key=mtu)
		//
		// ---
		//  type: integer
		//  default: MTU of the parent device
		//  managed: no
		//  shortdesc: The Maximum Transmit Unit (MTU) of the new interface
		"mtu",
	}

	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		// gendoc:generate(entity=devices, group=nic_physical, key=vlan)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: The VLAN ID to attach to
		optionalFields = append(optionalFields, "hwaddr", "vlan")
	}

	// checkWithManagedNetwork validates the device's settings against the managed network.
	checkWithManagedNetwork := func(n network.Network) error {
		if n.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Specified network is not fully created")
		}

		// NOTE: type is supposed to be 'physical'
		// if n.Type() != "bridge" {
		// 	return fmt.Errorf("Specified network must be of type bridge")
		// }

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

			ipAddr, _, err := net.ParseCIDR(parentAddress)
			if err != nil {
				return fmt.Errorf("Invalid network ipv4.address: %w", err)
			}

			if d.config["ipv4.address"] == "none" && util.IsFalseOrEmpty(d.config["security.ipv4_filtering"]) {
				return fmt.Errorf("Cannot have ipv4.address as none unless using security.ipv4_filtering")
			}

			// IP should not be the same as the parent managed network address.
			if ipAddr.Equal(net.ParseIP(d.config["ipv4.address"])) {
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

			ipAddr, _, err := net.ParseCIDR(parentAddress)
			if err != nil {
				return fmt.Errorf("Invalid network ipv6.address: %w", err)
			}

			if d.config["ipv6.address"] == "none" && util.IsFalseOrEmpty(d.config["security.ipv6_filtering"]) {
				return fmt.Errorf("Cannot have ipv6.address as none unless using security.ipv6_filtering")
			}

			// IP should not be the same as the parent managed network address.
			if ipAddr.Equal(net.ParseIP(d.config["ipv6.address"])) {
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

	var isParentBridge bool

	// gendoc:generate(entity=devices, group=nic_physical, key=network)
	//
	// ---
	//  type: string
	//  managed: no
	//  shortdesc: The managed network to link the device to (instead of specifying the `nictype` directly)
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "gvrp"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		// api.ProjectDefaultName is used here as physical networks don't support projects.
		var err error
		d.network, err = network.LoadByName(d.state, api.ProjectDefaultName, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		if d.network.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Specified network is not fully created")
		}

		if d.network.Type() != "physical" {
			return fmt.Errorf("Specified network must be of type physical")
		}

		netConfig := d.network.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// check if we have a bridge parent
		isParentBridge = util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"]))

		if isParentBridge {
			// Validate NIC settings with managed network.
			err = checkWithManagedNetwork(d.network)
			if err != nil {
				return err
			}

			if netConfig["bridge.mtu"] != "" {
				d.config["mtu"] = netConfig["bridge.mtu"]
			}
		}

		// Copy certain keys verbatim from the network's settings.
		for _, field := range optionalFields {
			_, found := netConfig[field]
			if found {
				d.config[field] = netConfig[field]
			}
		}
	} else if util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"])) {
		isParentBridge = true

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
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	if isParentBridge {
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

		// Check if security ACL(s) are configured.
		if d.config["security.acls"] != "" {
			if d.state.Firewall.String() != "nftables" {
				return fmt.Errorf("Security ACLs are only supported when using nftables firewall")
			}

			// The NIC's network may be a non-default project, so lookup project and get network's project name.
			networkProjectName, _, err := project.NetworkProject(d.state.DB.Cluster, instConf.Project().Name)
			if err != nil {
				return fmt.Errorf("Failed loading network project name: %w", err)
			}

			err = acl.Exists(d.state, networkProjectName, util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)...)
			if err != nil {
				return err
			}
		}

		// Add bridge validation rules
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
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// checkAddressConflict checks for conflicting IP/MAC addresses on another NIC connected to same network on the
// same cluster member. Can only validate this when the instance is supplied (and not doing profile validation).
// Returns api.StatusError with status code set to http.StatusConflict if conflicting address found.
func (d *nicPhysical) checkAddressConflict() error {
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
func (d *nicPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("Network physical devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	isParentBridge := util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"]))

	// initiate completely different startup sequence if parent is bridge
	if isParentBridge {
		// ensure bridge is managed
		if d.network == nil {
			return nil, fmt.Errorf("Error loading %s because it's unmanaged", d.config["parent"])
		}

		return d.startBridge()
	}

	saveData := make(map[string]string)

	reverter := revert.New()
	defer reverter.Fail()

	// pciIOMMUGroup, used for VM physical passthrough.
	var pciIOMMUGroup uint64

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = linux.LoadModule("vfio-pci")
		if err != nil {
			return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
		}
	}

	// Record the host_name device used for restoration later.
	saveData["host_name"] = network.GetHostDevice(d.config["parent"], d.config["vlan"])

	if d.inst.Type() == instancetype.Container {
		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], saveData["host_name"], d.config["vlan"], util.IsTrue(d.config["gvrp"]))
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		if util.IsTrue(saveData["last_state.created"]) {
			reverter.Add(func() {
				_ = networkRemoveInterfaceIfNeeded(d.state, saveData["host_name"], d.inst, d.config["parent"], d.config["vlan"])
			})
		}
		// If we didn't create the device we should track various properties so we can restore them when the
		// instance is stopped or the device is detached.
		if util.IsFalse(saveData["last_state.created"]) {
			err = networkSnapshotPhysicalNIC(saveData["host_name"], saveData)
			if err != nil {
				return nil, err
			}
		}

		// Set the MAC address.

		// gendoc:generate(entity=devices, group=nic_physical, key=hwaddr)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  managed: no
		//  shortdesc: The MAC address of the new interface
		if d.config["hwaddr"] != "" {
			hwaddr, err := net.ParseMAC(d.config["hwaddr"])
			if err != nil {
				return nil, fmt.Errorf("Failed parsing MAC address %q: %w", d.config["hwaddr"], err)
			}

			link := &ip.Link{Name: saveData["host_name"]}
			err = link.SetAddress(hwaddr)
			if err != nil {
				return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
			}
		}
		// IGNORE, VEth pair brings back mtu
		// Set the MTU.
		if d.config["mtu"] != "" {
			mtu, err := strconv.ParseUint(d.config["mtu"], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("Invalid MTU specified %q: %w", d.config["mtu"], err)
			}

			link := &ip.Link{Name: saveData["host_name"]}
			err = link.SetMTU(uint32(mtu))
			if err != nil {
				return nil, fmt.Errorf("Failed setting MTU %q on %q: %w", d.config["mtu"], saveData["host_name"], err)
			}
		}
	} else if d.inst.Type() == instancetype.VM {
		// Try to get PCI information about the network interface.
		ueventPath := fmt.Sprintf("/sys/class/net/%s/device/uevent", saveData["host_name"])
		pciDev, err := pcidev.ParseUeventFile(ueventPath)
		if err != nil {
			if errors.Is(err, pcidev.ErrDeviceIsUSB) {
				// Device is USB rather than PCI.
				return d.startVMUSB(saveData["host_name"])
			}

			return nil, fmt.Errorf("Failed to get PCI device info for %q: %w", saveData["host_name"], err)
		}

		saveData["last_state.pci.slot.name"] = pciDev.SlotName
		saveData["last_state.pci.driver"] = pciDev.Driver

		pciIOMMUGroup, err = pcidev.DeviceIOMMUGroup(saveData["last_state.pci.slot.name"])
		if err != nil {
			return nil, err
		}

		err = pcidev.DeviceDriverOverride(pciDev, "vfio-pci")
		if err != nil {
			return nil, err
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]}, // Will be our host-side VEth peer
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
				{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
			}...)
	}

	reverter.Success()

	return &runConf, nil
}

// Run through similar logic as nic bridged!
func (d *nicPhysical) startBridge() (*deviceConfig.RunConfig, error) {
	var err error
	var peerName string
	var mtu uint32

	saveData := make(map[string]string)

	reverter := revert.New()
	defer reverter.Fail()

	saveData["host_name"] = d.config["host_name"]

	// Create veth pair and configure peer end
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
		peerName = saveData["host_name"]
		mtu, err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	reverter.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

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
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Attach host side veth interface to bridge.
	err = network.AttachInterface(d.state, d.config["parent"], saveData["host_name"])
	if err != nil {
		return nil, err
	}

	reverter.Add(func() { _ = network.DetachInterface(d.state, d.config["parent"], saveData["host_name"]) })

	// Attempt to disable router advertisement acceptance.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// NOTE: This likely isn't needed b.c the documentation doesn't show it
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
	runConf.PostHooks = []func() error{d.bridgePostStart}

	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if d.config["io.bus"] == "usb" {
		runConf.UseUSBBus = true
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
			}...)
	}

	reverter.Success()

	return &runConf, nil
}

// bridgePostStart is run after the device is added to the instance.
func (d *nicPhysical) bridgePostStart() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

func (d *nicPhysical) startVMUSB(name string) (*deviceConfig.RunConfig, error) {
	// Get the list of network interfaces.
	interfaces, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	// Look for our USB device.
	var addr string
	for _, card := range interfaces.Cards {
		for _, port := range card.Ports {
			if port.ID == name {
				addr = card.USBAddress
				break
			}
		}

		if addr != "" {
			break
		}
	}

	if addr == "" {
		return nil, fmt.Errorf("Failed to get USB device info for %q", name)
	}

	// Parse the USB address.
	fields := strings.Split(addr, ":")
	if len(fields) != 2 {
		return nil, fmt.Errorf("Bad USB device info for %q", name)
	}

	usbBus, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("Bad USB device info for %q: %w", name, err)
	}

	usbDev, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("Bad USB device info for %q: %w", name, err)
	}

	// Record the addresses.
	saveData := map[string]string{}
	saveData["last_state.usb.bus"] = fmt.Sprintf("%03d", usbBus)
	saveData["last_state.usb.device"] = fmt.Sprintf("%03d", usbDev)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Generate a config.
	runConf := deviceConfig.RunConfig{}
	runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
		DeviceName:     fmt.Sprintf("%s-%03d-%03d", d.name, usbBus, usbDev),
		HostDevicePath: fmt.Sprintf("/dev/bus/usb/%03d/%03d", usbBus, usbDev),
	})

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicPhysical) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()

	isParentBridge := util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", v["parent"]))
	if isParentBridge {
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
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if v["last_state.usb.bus"] != "" && v["last_state.usb.device"] != "" {
		// Handle USB NICs.
		runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
			DeviceName:     fmt.Sprintf("%s-%s-%s", d.name, v["last_state.usb.bus"], v["last_state.usb.device"]),
			HostDevicePath: fmt.Sprintf("/dev/bus/usb/%s/%s", v["last_state.usb.bus"], v["last_state.usb.device"]),
		})
	} else {
		// Handle all other NICs.
		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.created":       "",
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"last_state.usb.bus":       "",
			"last_state.usb.device":    "",
		})
	}()

	v := d.volatileGet()

	isParentBridge := util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", v["parent"]))
	if isParentBridge {
		// Handle the case where validation fails but the device still must be removed.
		bridgeName := d.config["parent"]
		if bridgeName == "" && d.config["network"] != "" {
			bridgeName = d.config["network"]
		}

		networkVethFillFromVolatile(d.config, v)

		if d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
			// Detach host-side end of veth pair from bridge (required for openvswitch particularly).
			err := network.DetachInterface(d.state, bridgeName, d.config["host_name"])
			if err != nil {
				return fmt.Errorf("Failed to detach interface %q from %q: %w", d.config["host_name"], bridgeName, err)
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
		networkNICRouteDelete(bridgeName, routes...)
	} else {
		// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
		if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
			vfioDev := pcidev.Device{
				Driver:   "vfio-pci",
				SlotName: v["last_state.pci.slot.name"],
			}

			err := pcidev.DeviceDriverOverride(vfioDev, v["last_state.pci.driver"])
			if err != nil {
				return err
			}
		} else if d.inst.Type() == instancetype.Container {
			hostName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

			// This will delete the parent interface if we created it for VLAN parent.
			if util.IsTrue(v["last_state.created"]) {
				err := networkRemoveInterfaceIfNeeded(d.state, hostName, d.inst, d.config["parent"], d.config["vlan"])
				if err != nil {
					return err
				}
			} else if v["last_state.pci.slot.name"] == "" {
				err := networkRestorePhysicalNIC(hostName, v)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// rebuildDnsmasqEntry rebuilds the dnsmasq host entry if connected to a managed network and reloads dnsmasq.
func (d *nicPhysical) rebuildDnsmasqEntry() error {
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
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
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

// setupNativeBridgePortVLANs configures the bridge port with the specified VLAN settings on the native bridge.
func (d *nicPhysical) setupNativeBridgePortVLANs(hostName string) error {
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
func (d *nicPhysical) setupOVSBridgePortVLANs(hostName string) error {
	vswitch, err := d.state.OVS()
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
			vlanID, err := strconv.Atoi(d.config["vlan"])
			if err != nil {
				return err
			}

			err = vswitch.UpdateBridgePortVLANs(context.TODO(), hostName, "access", vlanID, nil)
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

		vlanMode := "trunk" // Default to only allowing tagged frames (drop untagged frames).
		if d.config["vlan"] != "none" {
			// If untagged vlan mode isn't "none" then allow untagged frames for port's 'native' VLAN.
			vlanMode = "native-untagged"
		}

		// Configure the tagged membership settings of the port if VLAN ID specified.
		// Also set the vlan_mode as needed from above.
		// Must come after the PortSet command used for setting "vlan" mode above so that the correct
		// vlan_mode is retained.
		err = vswitch.UpdateBridgePortVLANs(context.TODO(), hostName, vlanMode, 0, intNetworkVLANs)
		if err != nil {
			return err
		}
	}

	return nil
}
