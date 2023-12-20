package device

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/mdlayher/netx/eui64"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	pcidev "github.com/lxc/incus/internal/server/device/pci"
	"github.com/lxc/incus/internal/server/dnsmasq/dhcpalloc"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/network/acl"
	"github.com/lxc/incus/internal/server/network/ovn"
	"github.com/lxc/incus/internal/server/network/ovs"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/resources"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// ovnNet defines an interface for accessing instance specific functions on OVN network.
type ovnNet interface {
	network.Network

	InstanceDevicePortValidateExternalRoutes(deviceInstance instance.Instance, deviceName string, externalRoutes []*net.IPNet) error
	InstanceDevicePortAdd(instanceUUID string, deviceName string, deviceConfig deviceConfig.Device) error
	InstanceDevicePortStart(opts *network.OVNInstanceNICSetupOpts, securityACLsRemove []string) (ovn.OVNSwitchPort, []net.IP, error)
	InstanceDevicePortStop(ovsExternalOVNPort ovn.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error
	InstanceDevicePortRemove(instanceUUID string, deviceName string, deviceConfig deviceConfig.Device) error
	InstanceDevicePortIPs(instanceUUID string, deviceName string) ([]net.IP, error)
}

type nicOVN struct {
	deviceCommon

	network ovnNet // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *nicOVN) CanHotPlug() bool {
	return true
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicOVN) CanMigrate() bool {
	return true
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicOVN) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicOVN)
	if !match {
		return []string{}
	}

	return []string{"security.acls"}
}

// validateConfig checks the supplied config for correctness.
func (d *nicOVN) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{
		"network",
	}

	optionalFields := []string{
		"name",
		"hwaddr",
		"host_name",
		"mtu",
		"ipv4.address",
		"ipv6.address",
		"ipv4.routes",
		"ipv6.routes",
		"ipv4.routes.external",
		"ipv6.routes.external",
		"boot.priority",
		"security.acls",
		"security.acls.default.ingress.action",
		"security.acls.default.egress.action",
		"security.acls.default.ingress.logged",
		"security.acls.default.egress.logged",
		"acceleration",
		"nested",
		"vlan",
	}

	// The NIC's network may be a non-default project, so lookup project and get network's project name.
	networkProjectName, _, err := project.NetworkProject(d.state.DB.Cluster, instConf.Project().Name)
	if err != nil {
		return fmt.Errorf("Failed loading network project name: %w", err)
	}

	// Lookup network settings and apply them to the device's config.
	n, err := network.LoadByName(d.state, networkProjectName, d.config["network"])
	if err != nil {
		return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
	}

	if n.Status() != api.NetworkStatusCreated {
		return fmt.Errorf("Specified network is not fully created")
	}

	if n.Type() != "ovn" {
		return fmt.Errorf("Specified network must be of type ovn")
	}

	bannedKeys := []string{"mtu"}
	for _, bannedKey := range bannedKeys {
		if d.config[bannedKey] != "" {
			return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
		}
	}

	ovnNet, ok := n.(ovnNet)
	if !ok {
		return fmt.Errorf("Network is not ovnNet interface type")
	}

	d.network = ovnNet // Stored loaded network for use by other functions.
	netConfig := d.network.Config()

	if d.config["ipv4.address"] != "" {
		// Check that DHCPv4 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv4Subnet() == nil {
			return fmt.Errorf("Cannot specify %q when DHCP is disabled on network %q", "ipv4.address", d.config["network"])
		}

		ip, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
		if err != nil {
			return fmt.Errorf("Invalid network ipv4.address: %w", err)
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv4.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv4.address"], d.config["network"])
		}

		// IP should not be the same as the parent managed network address.
		if ip.Equal(net.ParseIP(d.config["ipv4.address"])) {
			return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv4.address"], d.config["parent"])
		}
	}

	if d.config["ipv6.address"] != "" {
		// Check that DHCPv6 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv6Subnet() == nil || util.IsFalseOrEmpty(netConfig["ipv6.dhcp.stateful"]) {
			return fmt.Errorf("Cannot specify %q when DHCP or %q are disabled on network %q", "ipv6.address", "ipv6.dhcp.stateful", d.config["network"])
		}

		// Static IPv6 is allowed only if static IPv4 is set as well.
		if d.config["ipv4.address"] == "" {
			return fmt.Errorf("Cannot specify %q when %q is not set", "ipv6.address", "ipv4.address")
		}

		ip, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
		if err != nil {
			return fmt.Errorf("Invalid network ipv6.address: %w", err)
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv6.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv6.address"], d.config["network"])
		}

		// IP should not be the same as the parent managed network address.
		if ip.Equal(net.ParseIP(d.config["ipv6.address"])) {
			return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv6.address"], d.config["parent"])
		}
	}

	// Apply network level config options to device config before validation.
	d.config["mtu"] = netConfig["bridge.mtu"]

	// Check VLAN ID is valid.
	if d.config["vlan"] != "" {
		nestedVLAN, err := strconv.ParseUint(d.config["vlan"], 10, 16)
		if err != nil {
			return fmt.Errorf("Invalid VLAN ID %q: %w", d.config["vlan"], err)
		}

		if nestedVLAN < 1 || nestedVLAN > 4095 {
			return fmt.Errorf("Invalid VLAN ID %q: Must be between 1 and 4095 inclusive", d.config["vlan"])
		}
	}

	// Perform checks that require instance (those not appropriate to do during profile validation).
	if d.inst != nil {
		// Check nested VLAN combination settings are valid. Requires instance for validation as settings
		// may come from a combination of profile and instance configs.
		if d.config["nested"] != "" {
			if d.config["vlan"] == "" {
				return fmt.Errorf("VLAN must be specified with a nested NIC")
			}

			// Check the NIC that this NIC is neted under exists on this instance and shares same
			// parent network.
			var nestedParentNIC string
			for devName, devConfig := range instConf.ExpandedDevices() {
				if devName != d.config["nested"] || devConfig["type"] != "nic" {
					continue
				}

				if devConfig["network"] != d.config["network"] {
					return fmt.Errorf("The nested parent NIC must be connected to same network as this NIC")
				}

				nestedParentNIC = devName
				break
			}

			if nestedParentNIC == "" {
				return fmt.Errorf("Instance does not have a NIC called %q for nesting under", d.config["nested"])
			}
		} else if d.config["vlan"] != "" {
			return fmt.Errorf("Specifying a VLAN requires that this NIC be nested")
		}

		// Check there isn't another NIC with any of the same addresses specified on the same network.
		// Can only validate this when the instance is supplied (and not doing profile validation).
		err := d.checkAddressConflict()
		if err != nil {
			return err
		}
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	// Now run normal validation.
	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	// Check IP external routes are within the network's external routes.
	var externalRoutes []*net.IPNet
	for _, k := range []string{"ipv4.routes.external", "ipv6.routes.external"} {
		if d.config[k] == "" {
			continue
		}

		externalRoutes, err = network.SubnetParseAppend(externalRoutes, util.SplitNTrimSpace(d.config[k], ",", -1, false)...)
		if err != nil {
			return err
		}
	}

	if len(externalRoutes) > 0 {
		err = d.network.InstanceDevicePortValidateExternalRoutes(d.inst, d.name, externalRoutes)
		if err != nil {
			return err
		}
	}

	// Check Security ACLs exist.
	if d.config["security.acls"] != "" {
		err = acl.Exists(d.state, networkProjectName, util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)...)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkAddressConflict checks for conflicting IP/MAC addresses on another NIC connected to same network.
// Can only validate this when the instance is supplied (and not doing profile validation).
// Returns api.StatusError with status code set to http.StatusConflict if conflicting address found.
func (d *nicOVN) checkAddressConflict() error {
	ourNICIPs := make(map[string]net.IP, 2)
	ourNICIPs["ipv4.address"] = net.ParseIP(d.config["ipv4.address"])
	ourNICIPs["ipv6.address"] = net.ParseIP(d.config["ipv6.address"])

	ourNICMAC, _ := net.ParseMAC(d.config["hwaddr"])
	if ourNICMAC == nil {
		ourNICMAC, _ = net.ParseMAC(d.volatileGet()["hwaddr"])
	}

	// Check if any instance devices use this network.
	return network.UsedByInstanceDevices(d.state, d.network.Project(), d.network.Name(), d.network.Type(), func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error {
		// Skip our own device. This avoids triggering duplicate device errors during
		// updates or when making temporary copies of our instance during migrations.
		sameLogicalInstance := instance.IsSameLogicalInstance(d.inst, &inst)
		if sameLogicalInstance && d.Name() == nicName {
			return nil
		}

		// Check there isn't another instance with the same DNS name connected to managed network.
		sameLogicalInstanceNestedNIC := sameLogicalInstance && (d.config["nested"] != "" || nicConfig["nested"] != "")
		if d.network != nil && !sameLogicalInstanceNestedNIC && nicCheckDNSNameConflict(d.inst.Name(), inst.Name) {
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
	})
}

// Add is run when a device is added to a non-snapshot instance whether or not the instance is running.
func (d *nicOVN) Add() error {
	return d.network.InstanceDevicePortAdd(d.inst.LocalConfig()["volatile.uuid"], d.name, d.config)
}

// PreStartCheck checks the managed parent network is available (if relevant).
func (d *nicOVN) PreStartCheck() error {
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

// validateEnvironment checks the runtime environment for correctness.
func (d *nicOVN) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	integrationBridge := d.state.GlobalConfig.NetworkOVNIntegrationBridge()

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", integrationBridge)) {
		return fmt.Errorf("OVS integration bridge device %q doesn't exist", integrationBridge)
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicOVN) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	// Load uplink network config.
	uplinkNetworkName := d.network.Config()["network"]
	_, uplink, _, err := d.state.DB.Cluster.GetNetworkInAnyState(api.ProjectDefaultName, uplinkNetworkName)
	if err != nil {
		return nil, fmt.Errorf("Failed to load uplink network %q: %w", uplinkNetworkName, err)
	}

	// Setup the host network interface (if not nested).
	var peerName, integrationBridgeNICName string
	var mtu uint32
	var vfPCIDev pcidev.Device
	var vDPADevice *ip.VDPADev
	var pciIOMMUGroup uint64

	if d.config["nested"] != "" {
		delete(saveData, "host_name") // Nested NICs don't have a host side interface.
	} else {
		if d.config["acceleration"] == "sriov" {
			vswitch, err := ovs.NewVSwitch()
			if err != nil {
				return nil, fmt.Errorf("Failed to connect to OVS: %w", err)
			}

			if !vswitch.HardwareOffloadingEnabled() {
				return nil, fmt.Errorf("SR-IOV acceleration requires hardware offloading be enabled in OVS")
			}

			// If VM, then try and load the vfio-pci module first.
			if d.inst.Type() == instancetype.VM {
				err := linux.LoadModule("vfio-pci")
				if err != nil {
					return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
				}
			}

			integrationBridge := d.state.GlobalConfig.NetworkOVNIntegrationBridge()

			// Find free VF exclusively.
			network.SRIOVVirtualFunctionMutex.Lock()
			vfParent, vfRepresentor, vfDev, vfID, err := network.SRIOVFindFreeVFAndRepresentor(d.state, integrationBridge)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return nil, fmt.Errorf("Failed finding a suitable free virtual function on %q: %w", integrationBridge, err)
			}

			// Claim the SR-IOV virtual function (VF) on the parent (PF) and get the PCI information.
			vfPCIDev, pciIOMMUGroup, err = networkSRIOVSetupVF(d.deviceCommon, vfParent, vfDev, vfID, false, saveData)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return nil, fmt.Errorf("Failed setting up VF: %w", err)
			}

			revert.Add(func() {
				_ = networkSRIOVRestoreVF(d.deviceCommon, false, saveData)
			})

			network.SRIOVVirtualFunctionMutex.Unlock()

			// Setup the guest network interface.
			if d.inst.Type() == instancetype.Container {
				err := networkSRIOVSetupContainerVFNIC(saveData["host_name"], d.config)
				if err != nil {
					return nil, fmt.Errorf("Failed setting up container VF NIC: %w", err)
				}
			}

			integrationBridgeNICName = vfRepresentor
			peerName = vfDev
		} else if d.config["acceleration"] == "vdpa" {
			vswitch, err := ovs.NewVSwitch()
			if err != nil {
				return nil, fmt.Errorf("Failed to connect to OVS: %w", err)
			}

			if !vswitch.HardwareOffloadingEnabled() {
				return nil, fmt.Errorf("SR-IOV acceleration requires hardware offloading be enabled in OVS")
			}

			err = linux.LoadModule("vdpa")
			if err != nil {
				return nil, fmt.Errorf("Error loading %q module: %w", "vdpa", err)
			}

			// If VM, then try and load the vhost_vdpa module first.
			if d.inst.Type() == instancetype.VM {
				err = linux.LoadModule("vhost_vdpa")
				if err != nil {
					return nil, fmt.Errorf("Error loading %q module: %w", "vhost_vdpa", err)
				}
			}

			integrationBridge := d.state.GlobalConfig.NetworkOVNIntegrationBridge()

			// Find free VF exclusively.
			network.SRIOVVirtualFunctionMutex.Lock()
			vfParent, vfRepresentor, vfDev, vfID, err := network.SRIOVFindFreeVFAndRepresentor(d.state, integrationBridge)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return nil, fmt.Errorf("Failed finding a suitable free virtual function on %q: %w", integrationBridge, err)
			}

			// Claim the SR-IOV virtual function (VF) on the parent (PF) and get the PCI information.
			vfPCIDev, pciIOMMUGroup, err = networkSRIOVSetupVF(d.deviceCommon, vfParent, vfDev, vfID, false, saveData)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return nil, err
			}

			revert.Add(func() {
				_ = networkSRIOVRestoreVF(d.deviceCommon, false, saveData)
			})

			// Create the vDPA management device
			vDPADevice, err = ip.AddVDPADevice(vfPCIDev.SlotName, saveData)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return nil, err
			}

			network.SRIOVVirtualFunctionMutex.Unlock()

			// Setup the guest network interface.
			if d.inst.Type() == instancetype.Container {
				return nil, fmt.Errorf("VDPA acceleration is not supported for containers")
			}

			integrationBridgeNICName = vfRepresentor
			peerName = vfDev
		} else {
			// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
			if d.inst.Type() == instancetype.Container {
				if saveData["host_name"] == "" {
					saveData["host_name"], err = d.generateHostName("veth", d.config["hwaddr"])
					if err != nil {
						return nil, err
					}
				}

				integrationBridgeNICName = saveData["host_name"]
				peerName, mtu, err = networkCreateVethPair(saveData["host_name"], d.config)
				if err != nil {
					return nil, err
				}
			} else if d.inst.Type() == instancetype.VM {
				if saveData["host_name"] == "" {
					saveData["host_name"], err = d.generateHostName("tap", d.config["hwaddr"])
					if err != nil {
						return nil, err
					}
				}

				integrationBridgeNICName = saveData["host_name"]
				peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
				mtu, err = networkCreateTap(saveData["host_name"], d.config)
				if err != nil {
					return nil, err
				}
			}

			revert.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })
		}
	}

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	v := d.volatileGet()

	// Retrieve any last state IPs from volatile and pass them to OVN driver for potential use with sticky
	// DHCPv4 allocations.
	var lastStateIPs []net.IP
	for _, ipStr := range util.SplitNTrimSpace(v["last_state.ip_addresses"], ",", -1, true) {
		lastStateIP := net.ParseIP(ipStr)
		if lastStateIP != nil {
			lastStateIPs = append(lastStateIPs, lastStateIP)
		}
	}

	// Add new OVN logical switch port for instance.
	logicalPortName, dnsIPs, err := d.network.InstanceDevicePortStart(&network.OVNInstanceNICSetupOpts{
		InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
		DNSName:      d.inst.Name(),
		DeviceName:   d.name,
		DeviceConfig: d.config,
		UplinkConfig: uplink.Config,
		LastStateIPs: lastStateIPs, // Pass in volatile last state IPs for use with sticky DHCPv4 hint.
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed setting up OVN port: %w", err)
	}

	// Record switch port DNS IPs to volatile so they can be used as sticky DHCPv4 hint in the future in order
	// to allocate the same IPs on next start if they are still available/appropriate.
	// This volatile key will not be removed when instance stops.
	var dnsIPsStr strings.Builder
	for i, dnsIP := range dnsIPs {
		if i > 0 {
			dnsIPsStr.WriteString(",")
		}

		dnsIPsStr.WriteString(dnsIP.String())
	}

	saveData["last_state.ip_addresses"] = dnsIPsStr.String()

	revert.Add(func() {
		_ = d.network.InstanceDevicePortStop("", &network.OVNInstanceNICStopOpts{
			InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
			DeviceName:   d.name,
			DeviceConfig: d.config,
		})
	})

	// Associated host side interface to OVN logical switch port (if not nested).
	if integrationBridgeNICName != "" {
		cleanup, err := d.setupHostNIC(integrationBridgeNICName, logicalPortName, uplink)
		if err != nil {
			return nil, err
		}

		revert.Add(cleanup)
	}

	runConf := deviceConfig.RunConfig{}

	// Get local chassis ID for chassis group.
	vswitch, err := ovs.NewVSwitch()
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to OVS: %w", err)
	}

	chassisID, err := vswitch.ChassisID()
	if err != nil {
		return nil, fmt.Errorf("Failed getting OVS Chassis ID: %w", err)
	}

	ovnClient, err := ovn.NewNB(d.state)
	if err != nil {
		return nil, fmt.Errorf("Failed to get OVN client: %w", err)
	}

	// Add post start hook for setting logical switch port chassis once instance has been started.
	runConf.PostHooks = append(runConf.PostHooks, func() error {
		err := ovnClient.LogicalSwitchPortOptionsSet(logicalPortName, map[string]string{"requested-chassis": chassisID})
		if err != nil {
			return fmt.Errorf("Failed setting logical switch port chassis ID: %w", err)
		}

		return nil
	})

	runConf.PostHooks = append(runConf.PostHooks, d.postStart)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Return instance network interface configuration (if not nested).
	if saveData["host_name"] != "" {
		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "type", Value: "phys"},
			{Key: "name", Value: d.config["name"]},
			{Key: "flags", Value: "up"},
			{Key: "link", Value: peerName},
		}

		instType := d.inst.Type()
		if instType == instancetype.VM {
			if d.config["acceleration"] == "sriov" {
				runConf.NetworkInterface = append(runConf.NetworkInterface,
					[]deviceConfig.RunConfigItem{
						{Key: "devName", Value: d.name},
						{Key: "pciSlotName", Value: vfPCIDev.SlotName},
						{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
						{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
					}...)
			} else if d.config["acceleration"] == "vdpa" {
				if vDPADevice == nil {
					return nil, fmt.Errorf("vDPA device is nil")
				}

				runConf.NetworkInterface = append(runConf.NetworkInterface,
					[]deviceConfig.RunConfigItem{
						{Key: "devName", Value: d.name},
						{Key: "pciSlotName", Value: vfPCIDev.SlotName},
						{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
						{Key: "maxVQP", Value: fmt.Sprintf("%d", vDPADevice.MaxVQs/2)},
						{Key: "vDPADevName", Value: vDPADevice.Name},
						{Key: "vhostVDPAPath", Value: vDPADevice.VhostVDPA.Path},
						{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
					}...)
			} else {
				runConf.NetworkInterface = append(runConf.NetworkInterface,
					[]deviceConfig.RunConfigItem{
						{Key: "devName", Value: d.name},
						{Key: "hwaddr", Value: d.config["hwaddr"]},
						{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
					}...)
			}
		} else if instType == instancetype.Container {
			runConf.NetworkInterface = append(runConf.NetworkInterface,
				deviceConfig.RunConfigItem{Key: "hwaddr", Value: d.config["hwaddr"]},
			)
		}
	}

	revert.Success()
	return &runConf, nil
}

// postStart is run after the device is added to the instance.
func (d *nicOVN) postStart() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *nicOVN) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	// If an IPv6 address has changed, if the instance is running we should bounce the host-side
	// veth interface to give the instance a chance to detect the change and re-apply for an
	// updated lease with new IP address.
	if d.config["ipv6.address"] != oldConfig["ipv6.address"] && d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
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

	// Apply any changes needed when assigned ACLs change.
	if d.config["security.acls"] != oldConfig["security.acls"] {
		// Work out which ACLs have been removed and remove logical port from those groups.
		oldACLs := util.SplitNTrimSpace(oldConfig["security.acls"], ",", -1, true)
		newACLs := util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)
		removedACLs := []string{}
		for _, oldACL := range oldACLs {
			if !util.ValueInSlice(oldACL, newACLs) {
				removedACLs = append(removedACLs, oldACL)
			}
		}

		// Setup the logical port with new ACLs if running.
		if isRunning {
			// Load uplink network config.
			uplinkNetworkName := d.network.Config()["network"]
			_, uplink, _, err := d.state.DB.Cluster.GetNetworkInAnyState(api.ProjectDefaultName, uplinkNetworkName)
			if err != nil {
				return fmt.Errorf("Failed to load uplink network %q: %w", uplinkNetworkName, err)
			}

			// Update OVN logical switch port for instance.
			_, _, err = d.network.InstanceDevicePortStart(&network.OVNInstanceNICSetupOpts{
				InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
				DNSName:      d.inst.Name(),
				DeviceName:   d.name,
				DeviceConfig: d.config,
				UplinkConfig: uplink.Config,
			}, removedACLs)
			if err != nil {
				return fmt.Errorf("Failed updating OVN port: %w", err)
			}
		}

		if len(removedACLs) > 0 {
			client, err := ovn.NewNB(d.state)
			if err != nil {
				return fmt.Errorf("Failed to get OVN client: %w", err)
			}

			err = acl.OVNPortGroupDeleteIfUnused(d.state, d.logger, client, d.network.Project(), d.inst, d.name, newACLs...)
			if err != nil {
				return fmt.Errorf("Failed removing unused OVN port groups: %w", err)
			}
		}
	}

	// If an external address changed, update the BGP advertisements.
	err := bgpRemovePrefix(&d.deviceCommon, oldConfig)
	if err != nil {
		return err
	}

	err = bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

func (d *nicOVN) findRepresentorPort(volatile map[string]string) (string, error) {
	physSwitchID, pfID, err := network.SRIOVGetSwitchAndPFID(volatile["last_state.vf.parent"])
	if err != nil {
		return "", fmt.Errorf("Failed finding physical parent switch and PF ID to release representor port: %w", err)
	}

	sysClassNet := "/sys/class/net"
	nics, err := os.ReadDir(sysClassNet)
	if err != nil {
		return "", fmt.Errorf("Failed reading NICs directory %q: %w", sysClassNet, err)
	}

	vfID, err := strconv.Atoi(volatile["last_state.vf.id"])
	if err != nil {
		return "", fmt.Errorf("Failed parsing last VF ID %q: %w", volatile["last_state.vf.id"], err)
	}

	// Track down the representor port to remove it from the integration bridge.
	representorPort := network.SRIOVFindRepresentorPort(nics, string(physSwitchID), pfID, vfID)
	if representorPort == "" {
		return "", fmt.Errorf("Failed finding representor")
	}

	return representorPort, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicOVN) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	v := d.volatileGet()

	var err error

	// Try and retrieve the last associated OVN switch port for the instance interface in the local OVS DB.
	// If we cannot get this, don't fail, as InstanceDevicePortStop will then try and generate the likely
	// port name using the same regime it does for new ports. This part is only here in order to allow
	// instance ports generated under an older regime to be cleaned up properly.
	networkVethFillFromVolatile(d.config, v)
	vswitch, err := ovs.NewVSwitch()
	if err != nil {
		d.logger.Error("Failed to connect to OVS", logger.Ctx{"err": err})
	}

	var ovsExternalOVNPort string
	if d.config["nested"] == "" {
		ovsExternalOVNPort, err = vswitch.InterfaceAssociatedOVNSwitchPort(d.config["host_name"])
		if err != nil {
			d.logger.Warn("Could not find OVN Switch port associated to OVS interface", logger.Ctx{"interface": d.config["host_name"]})
		}
	}

	integrationBridgeNICName := d.config["host_name"]
	if d.config["acceleration"] == "sriov" || d.config["acceleration"] == "vdpa" {
		integrationBridgeNICName, err = d.findRepresentorPort(v)
		if err != nil {
			d.logger.Error("Failed finding representor port to detach from OVS integration bridge", logger.Ctx{"err": err})
		}
	}

	// If there is integrationBridgeNICName specified, then try and remove it from the OVS integration bridge.
	// Do this early on during the stop process to prevent any future error from leaving the OVS port present
	// as if the instance is being migrated, this can cause port conflicts in OVN if the instance comes up on
	// another host later.
	if integrationBridgeNICName != "" {
		integrationBridge := d.state.GlobalConfig.NetworkOVNIntegrationBridge()

		// Detach host-side end of veth pair from OVS integration bridge.
		err = vswitch.BridgePortDelete(integrationBridge, integrationBridgeNICName)
		if err != nil {
			// Don't fail here as we want the postStop hook to run to clean up the local veth pair.
			d.logger.Error("Failed detaching interface from OVS integration bridge", logger.Ctx{"interface": integrationBridgeNICName, "bridge": integrationBridge, "err": err})
		}
	}

	instanceUUID := d.inst.LocalConfig()["volatile.uuid"]
	err = d.network.InstanceDevicePortStop(ovn.OVNSwitchPort(ovsExternalOVNPort), &network.OVNInstanceNICStopOpts{
		InstanceUUID: instanceUUID,
		DeviceName:   d.name,
		DeviceConfig: d.config,
	})
	if err != nil {
		// Don't fail here as we still want the postStop hook to run to clean up the local veth pair.
		d.logger.Error("Failed to remove OVN device port", logger.Ctx{"err": err})
	}

	// Remove BGP announcements.
	err = bgpRemovePrefix(&d.deviceCommon, d.config)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicOVN) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.created":       "",
			"last_state.vdpa.name":     "",
			"last_state.vf.parent":     "",
			"last_state.vf.id":         "",
			"last_state.vf.hwaddr":     "",
			"last_state.vf.vlan":       "",
			"last_state.vf.spoofcheck": "",
			"last_state.pci.driver":    "",
		})
	}()

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["acceleration"] == "sriov" {
		// Restoring host-side interface.
		network.SRIOVVirtualFunctionMutex.Lock()
		err := networkSRIOVRestoreVF(d.deviceCommon, false, v)
		if err != nil {
			network.SRIOVVirtualFunctionMutex.Unlock()
			return err
		}

		network.SRIOVVirtualFunctionMutex.Unlock()

		link := &ip.Link{Name: d.config["host_name"]}
		err = link.SetDown()
		if err != nil {
			return fmt.Errorf("Failed to bring down the host interface %s: %w", d.config["host_name"], err)
		}
	} else if d.config["acceleration"] == "vdpa" {
		// Retrieve the last state vDPA device name.
		network.SRIOVVirtualFunctionMutex.Lock()
		vDPADevName, ok := v["last_state.vdpa.name"]
		if !ok {
			network.SRIOVVirtualFunctionMutex.Unlock()
			return fmt.Errorf("Failed to find PCI slot name for vDPA device")
		}

		// Delete the vDPA management device.
		err := ip.DeleteVDPADevice(vDPADevName)
		if err != nil {
			network.SRIOVVirtualFunctionMutex.Unlock()
			return err
		}

		// Restoring host-side interface.
		network.SRIOVVirtualFunctionMutex.Lock()
		err = networkSRIOVRestoreVF(d.deviceCommon, false, v)
		if err != nil {
			network.SRIOVVirtualFunctionMutex.Unlock()
			return err
		}

		network.SRIOVVirtualFunctionMutex.Unlock()

		link := &ip.Link{Name: d.config["host_name"]}
		err = link.SetDown()
		if err != nil {
			return fmt.Errorf("Failed to bring down the host interface %q: %w", d.config["host_name"], err)
		}
	} else if d.config["host_name"] != "" && util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %q: %w", d.config["host_name"], err)
		}
	}

	return nil
}

// Remove is run when the device is removed from the instance or the instance is deleted.
func (d *nicOVN) Remove() error {
	// Check for port groups that will become unused (and need deleting) as this NIC is deleted.
	securityACLs := util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)
	if len(securityACLs) > 0 {
		client, err := ovn.NewNB(d.state)
		if err != nil {
			return fmt.Errorf("Failed to get OVN client: %w", err)
		}

		err = acl.OVNPortGroupDeleteIfUnused(d.state, d.logger, client, d.network.Project(), d.inst, d.name)
		if err != nil {
			return fmt.Errorf("Failed removing unused OVN port groups: %w", err)
		}
	}

	return d.network.InstanceDevicePortRemove(d.inst.LocalConfig()["volatile.uuid"], d.name, d.config)
}

// State gets the state of an OVN NIC by querying the OVN Northbound logical switch port record.
func (d *nicOVN) State() (*api.InstanceStateNetwork, error) {
	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	addresses := []api.InstanceStateNetworkAddress{}
	netConfig := d.network.Config()

	// Extract subnet sizes from bridge addresses.
	_, v4subnet, _ := net.ParseCIDR(netConfig["ipv4.address"])
	_, v6subnet, _ := net.ParseCIDR(netConfig["ipv6.address"])

	var v4mask string
	if v4subnet != nil {
		mask, _ := v4subnet.Mask.Size()
		v4mask = fmt.Sprintf("%d", mask)
	}

	var v6mask string
	if v6subnet != nil {
		mask, _ := v6subnet.Mask.Size()
		v6mask = fmt.Sprintf("%d", mask)
	}

	// OVN only supports dynamic IP allocation if neither IPv4 or IPv6 are statically set.
	if d.config["ipv4.address"] == "" && d.config["ipv6.address"] == "" {
		instanceUUID := d.inst.LocalConfig()["volatile.uuid"]
		devIPs, err := d.network.InstanceDevicePortIPs(instanceUUID, d.name)
		if err == nil {
			for _, devIP := range devIPs {
				family := "inet"
				netmask := v4mask

				if devIP.To4() == nil {
					family = "inet6"
					netmask = v6mask
				}

				addresses = append(addresses, api.InstanceStateNetworkAddress{
					Family:  family,
					Address: devIP.String(),
					Netmask: netmask,
					Scope:   "global",
				})
			}
		} else {
			d.logger.Warn("Failed getting OVN port device IPs", logger.Ctx{"err": err})
		}
	} else {
		if d.config["ipv4.address"] != "" {
			// Static DHCPv4 allocation present, that is likely to be the NIC's IPv4. So assume that.
			addresses = append(addresses, api.InstanceStateNetworkAddress{
				Family:  "inet",
				Address: d.config["ipv4.address"],
				Netmask: v4mask,
				Scope:   "global",
			})
		}

		if d.config["ipv6.address"] != "" {
			// Static DHCPv6 allocation present, that is likely to be the NIC's IPv6. So assume that.
			addresses = append(addresses, api.InstanceStateNetworkAddress{
				Family:  "inet6",
				Address: d.config["ipv6.address"],
				Netmask: v6mask,
				Scope:   "global",
			})
		} else if util.IsFalseOrEmpty(netConfig["ipv6.dhcp.stateful"]) && d.config["hwaddr"] != "" && v6subnet != nil {
			// If no static DHCPv6 allocation and stateful DHCPv6 is disabled, and IPv6 is enabled on
			// the bridge, the NIC is likely to use its MAC and SLAAC to configure its address.
			hwAddr, err := net.ParseMAC(d.config["hwaddr"])
			if err == nil {
				ip, err := eui64.ParseMAC(v6subnet.IP, hwAddr)
				if err == nil {
					addresses = append(addresses, api.InstanceStateNetworkAddress{
						Family:  "inet6",
						Address: ip.String(),
						Netmask: v6mask,
						Scope:   "global",
					})
				}
			}
		}
	}

	// Get MTU of host interface that connects to OVN integration bridge if exists.
	iface, err := net.InterfaceByName(d.config["host_name"])
	if err != nil {
		d.logger.Warn("Failed getting host interface state for MTU", logger.Ctx{"host_name": d.config["host_name"], "err": err})
	}

	mtu := -1
	if iface != nil {
		mtu = iface.MTU
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

// Register sets up anything needed on startup.
func (d *nicOVN) Register() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

func (d *nicOVN) setupHostNIC(hostName string, ovnPortName ovn.OVNSwitchPort, uplink *api.Network) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Disable IPv6 on host-side veth interface (prevents host-side interface getting link-local address and
	// accepting router advertisements) as not needed because the host-side interface is connected to a bridge.
	err := localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", hostName), "1")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Attempt to disable IPv4 forwarding.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv4/conf/%s/forwarding", hostName), "0")
	if err != nil {
		return nil, err
	}

	// Attach host side veth interface to bridge.
	integrationBridge := d.state.GlobalConfig.NetworkOVNIntegrationBridge()

	vswitch, err := ovs.NewVSwitch()
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to OVS: %w", err)
	}

	err = vswitch.BridgePortAdd(integrationBridge, hostName, true)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = vswitch.BridgePortDelete(integrationBridge, hostName) })

	// Link OVS port to OVN logical port.
	err = vswitch.InterfaceAssociateOVNSwitchPort(hostName, string(ovnPortName))
	if err != nil {
		return nil, err
	}

	// Make sure the port is up.
	link := &ip.Link{Name: hostName}
	err = link.SetUp()
	if err != nil {
		return nil, fmt.Errorf("Failed to bring up the host interface %s: %w", hostName, err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, err
}
