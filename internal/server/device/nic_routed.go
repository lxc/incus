package device

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"time"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

var nicRoutedIPGateway = map[string]net.IP{
	"ipv4": net.IPv4(169, 254, 0, 1),                                  // 169.254.0.1
	"ipv6": {0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}, // fe80::1
}

type nicRouted struct {
	deviceCommon
	effectiveParentName string
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *nicRouted) CanHotPlug() bool {
	return true
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicRouted) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicRouted)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.priority"}
}

// validateConfig checks the supplied config for correctness.
func (d *nicRouted) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	err := d.isUniqueWithGatewayAutoMode(instConf)
	if err != nil {
		return err
	}

	requiredFields := []string{}
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_routed, key=name)
		//
		// ---
		//  type: string
		//  default: kernel assigned
		//  shortdesc: The name of the interface inside the instance
		"name",

		// gendoc:generate(entity=devices, group=nic_routed, key=parent)
		//
		// ---
		//  type: string
		//  shortdesc: The name of the parent host device to join the instance to
		"parent",

		// gendoc:generate(entity=devices, group=nic_routed, key=mtu)
		//
		// ---
		//  type: integer
		//  default: parent MTU
		//  shortdesc: The Maximum Transmit Unit (MTU) of the new interface
		"mtu",

		// gendoc:generate(entity=devices, group=nic_routed, key=queue.tx.length)
		//
		// ---
		//  type: integer
		//  shortdesc: The transmit queue length for the NIC
		"queue.tx.length",

		// gendoc:generate(entity=devices, group=nic_routed, key=hwaddr)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  shortdesc: The MAC address of the new interface
		"hwaddr",

		// gendoc:generate(entity=devices, group=nic_routed, key=host_name)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  shortdesc: The name of the interface on the host
		"host_name",

		// gendoc:generate(entity=devices, group=nic_routed, key=vlan)
		//
		// ---
		//  type: integer
		//  shortdesc: The VLAN ID to attach to
		"vlan",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.ingress)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}instances-limit-units)
		"limits.ingress",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.egress)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}instances-limit-units)
		"limits.egress",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.max)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for both incoming and outgoing traffic (same as setting both limits.ingress and limits.egress)
		"limits.max",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.priority)
		//
		// ---
		//  type: integer
		//  shortdesc: The priority for outgoing traffic, to be used by the kernel queuing discipline to prioritize network packets
		"limits.priority",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.gateway)
		//
		// ---
		//  type: string
		//  default: auto
		//  shortdesc: Whether to add an automatic default IPv4 gateway (can be `auto` or `none`)
		"ipv4.gateway",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.gateway)
		//
		// ---
		//  type: string
		//  default: auto
		//  shortdesc: Whether to add an automatic default IPv6 gateway (can be `auto` or `none`)
		"ipv6.gateway",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.routes)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-delimited list of IPv4 static routes to add on host to NIC (without L2 ARP/NDP proxy)
		"ipv4.routes",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.routes)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-delimited list of IPv6 static routes to add on host to NIC (without L2 ARP/NDP proxy)
		"ipv6.routes",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.host_address)
		//
		// ---
		//  type: string
		//  default: `169.254.0.1`
		//  shortdesc: The IPv4 address to add to the host-side `veth` interface
		"ipv4.host_address",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.host_address)
		//
		// ---
		//  type: string
		//  default: `fe80::1`
		//  shortdesc: The IPv6 address to add to the host-side `veth` interface
		"ipv6.host_address",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.host_table)
		//
		// The custom policy routing table ID to add IPv4 static routes to (in addition to the main routing table)
		//
		// ---
		//  type: integer
		//  shortdesc: Deprecated: Use `ipv4.host_tables` instead
		"ipv4.host_table",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.host_table)
		//
		// The custom policy routing table ID to add IPv6 static routes to (in addition to the main routing table)
		//
		// ---
		//  type: integer
		//  shortdesc: Deprecated: Use `ipv6.host_tables` instead
		"ipv6.host_table",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.host_tables)
		//
		// ---
		//  type: string
		//  default: 254
		//  shortdesc: Comma-delimited list of routing tables IDs to add IPv4 static routes to
		"ipv4.host_tables",

		// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.host_tables)
		//
		// ---
		//  type: string
		//  default: 254
		//  shortdesc: Comma-delimited list of routing tables IDs to add IPv6 static routes to
		"ipv6.host_tables",

		// gendoc:generate(entity=devices, group=nic_routed, key=gvrp)
		//
		// ---
		//  type: bool
		//  default: false
		//  shortdesc: Register VLAN using GARP VLAN Registration Protocol
		"gvrp",

		// gendoc:generate(entity=devices, group=nic_routed, key=vrf)
		//
		// ---
		//  type: string
		//  shortdesc: The VRF on the host in which the host-side interface and routes are created
		"vrf",

		// gendoc:generate(entity=devices, group=nic_routed, key=io.bus)
		//
		// ---
		//  type: string
		//  default: `virtio`
		//  shortdesc: Override the bus for the device (can be `virtio` or `usb`) (VM only)
		"io.bus",
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.address)
	//
	// ---
	//  type: string
	//  shortdesc: Comma-delimited list of IPv4 static addresses to add to the instance
	rules["ipv4.address"] = validate.Optional(validate.IsListOf(validate.IsNetworkAddressV4))

	// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.address)
	//
	// ---
	//  type: string
	//  shortdesc: Comma-delimited list of IPv6 static addresses to add to the instance
	rules["ipv6.address"] = validate.Optional(validate.IsListOf(validate.IsNetworkAddressV6))

	// gendoc:generate(entity=devices, group=nic_routed, key=ipv4.neighbor_probe)
	//
	// ---
	//  type: bool
	//  default: true
	//  shortdesc: Whether to probe the parent network for IP address availability
	rules["ipv4.neighbor_probe"] = validate.Optional(validate.IsBool)

	// gendoc:generate(entity=devices, group=nic_routed, key=ipv6.neighbor_probe)
	//
	// ---
	//  type: bool
	//  default: true
	//  shortdesc: Whether to probe the parent network for IP address availability
	rules["ipv6.neighbor_probe"] = validate.Optional(validate.IsBool)

	rules["ipv4.host_tables"] = validate.Optional(validate.IsListOf(validate.IsInRange(0, 255)))
	rules["ipv6.host_tables"] = validate.Optional(validate.IsListOf(validate.IsInRange(0, 255)))
	rules["gvrp"] = validate.Optional(validate.IsBool)
	rules["vrf"] = validate.Optional(validate.IsAny)

	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	// Detect duplicate IPs in config.
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		ips := make(map[string]struct{})

		if d.config[key] != "" {
			for _, addr := range strings.Split(d.config[key], ",") {
				addr = strings.TrimSpace(addr)
				_, dupe := ips[addr]
				if dupe {
					return fmt.Errorf("Duplicate address %q in %q", addr, key)
				}

				ips[addr] = struct{}{}
			}
		}
	}

	// Ensure that address is set if routes is set.
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		if d.config[fmt.Sprintf("%s.routes", keyPrefix)] != "" && d.config[fmt.Sprintf("%s.address", keyPrefix)] == "" {
			return fmt.Errorf("%s.routes requires %s.address to be set", keyPrefix, keyPrefix)
		}
	}

	// Ensure that VLAN setting is only used with parent setting.
	if d.config["parent"] == "" && d.config["vlan"] != "" {
		return errors.New("The vlan setting can only be used when combined with a parent interface")
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicRouted) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return errors.New("Requires name property to start")
	}

	if d.config["parent"] != "" {
		// Check parent interface exists (don't use d.effectiveParentName here as we want to check the
		// parent of any VLAN interface exists too). The VLAN interface will be created later if needed.
		if !network.InterfaceExists(d.config["parent"]) {
			return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
		}

		// Detect the effective parent interface that we will be using (taking into account VLAN setting).
		d.effectiveParentName = network.GetHostDevice(d.config["parent"], d.config["vlan"])

		// If the effective parent doesn't exist and the vlan option is specified, it means we are going to
		// create the VLAN parent at start, and we will configure the needed sysctls then, so skip checks
		// on the effective parent.
		if d.config["vlan"] != "" && !network.InterfaceExists(d.effectiveParentName) {
			return nil
		}

		// Check necessary "all" sysctls are configured for use with l2proxy parent for routed mode.
		if d.config["ipv6.address"] != "" {
			// net.ipv6.conf.all.forwarding=1 is required to enable general packet forwarding for IPv6.
			ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", "all")
			sysctlVal, err := localUtil.SysctlGet(ipv6FwdPath)
			if err != nil {
				return fmt.Errorf("Error reading net sysctl %s: %w", ipv6FwdPath, err)
			}

			if sysctlVal != "1\n" {
				return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.forwarding=1", "all")
			}

			// net.ipv6.conf.all.proxy_ndp=1 is needed otherwise unicast neighbour solicitations are .
			// rejected This causes periodic latency spikes every 15-20s as the neighbour has to resort
			// to using multicast NDP resolution and expires the previous neighbour entry.
			ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", "all")
			sysctlVal, err = localUtil.SysctlGet(ipv6ProxyNdpPath)
			if err != nil {
				return fmt.Errorf("Error reading net sysctl %s: %w", ipv6ProxyNdpPath, err)
			}

			if sysctlVal != "1\n" {
				return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", "all")
			}
		}

		// Check necessary sysctls are configured for use with l2proxy parent for routed mode.
		if d.config["ipv4.address"] != "" {
			ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", d.effectiveParentName)
			sysctlVal, err := localUtil.SysctlGet(ipv4FwdPath)
			if err != nil {
				return fmt.Errorf("Error reading net sysctl %s: %w", ipv4FwdPath, err)
			}

			if sysctlVal != "1\n" {
				// Replace . in parent name with / for sysctl formatting.
				return fmt.Errorf("Routed mode requires sysctl net.ipv4.conf.%s.forwarding=1", strings.ReplaceAll(d.effectiveParentName, ".", "/"))
			}
		}

		// Check necessary device specific sysctls are configured for use with l2proxy parent for routed mode.
		if d.config["ipv6.address"] != "" {
			ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", d.effectiveParentName)
			sysctlVal, err := localUtil.SysctlGet(ipv6FwdPath)
			if err != nil {
				return fmt.Errorf("Error reading net sysctl %s: %w", ipv6FwdPath, err)
			}

			if sysctlVal != "1\n" {
				// Replace . in parent name with / for sysctl formatting.
				return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.forwarding=1", strings.ReplaceAll(d.effectiveParentName, ".", "/"))
			}

			ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", d.effectiveParentName)
			sysctlVal, err = localUtil.SysctlGet(ipv6ProxyNdpPath)
			if err != nil {
				return fmt.Errorf("Error reading net sysctl %s: %w", ipv6ProxyNdpPath, err)
			}

			if sysctlVal != "1\n" {
				// Replace . in parent name with / for sysctl formatting.
				return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", strings.ReplaceAll(d.effectiveParentName, ".", "/"))
			}
		}
	}

	if d.config["vrf"] != "" {
		// Check if the vrf interface exists.
		if !network.InterfaceExists(d.config["vrf"]) {
			return fmt.Errorf("VRF %q doesn't exist", d.config["vrf"])
		}
	}

	return nil
}

// checkIPAvailability checks using ARP and NDP neighbour probes whether any of the NIC's IPs are already in use.
func (d *nicRouted) checkIPAvailability(parent string) error {
	var addresses []net.IP

	if util.IsTrueOrEmpty(d.config["ipv4.neighbor_probe"]) {
		ipv4Addrs := util.SplitNTrimSpace(d.config["ipv4.address"], ",", -1, true)
		for _, addr := range ipv4Addrs {
			addresses = append(addresses, net.ParseIP(addr))
		}
	}

	if util.IsTrueOrEmpty(d.config["ipv6.neighbor_probe"]) {
		ipv6Addrs := util.SplitNTrimSpace(d.config["ipv6.address"], ",", -1, true)
		for _, addr := range ipv6Addrs {
			addresses = append(addresses, net.ParseIP(addr))
		}
	}

	errs := make(chan error, len(addresses))
	for _, address := range addresses {
		go func(address net.IP) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			inUse, err := isIPAvailable(ctx, address, parent)
			if err != nil {
				d.logger.Warn("Failed checking IP address available on parent network", logger.Ctx{"IP": address, "parent": parent, "err": err})
			}

			if inUse {
				errs <- fmt.Errorf("IP address %q in use on parent network %q", address, parent)
			} else {
				errs <- nil
			}
		}(address)
	}

	for range addresses {
		err := <-errs
		if err != nil {
			return err
		}
	}

	return nil
}

// Start is run when the instance is starting up (Routed mode doesn't support hot plugging).
func (d *nicRouted) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	reverter := revert.New()
	defer reverter.Fail()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	if d.config["vlan"] != "" {
		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], d.effectiveParentName, d.config["vlan"], util.IsTrue(d.config["gvrp"]))
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		// If we created a VLAN interface, we need to setup the sysctls on that interface.
		if util.IsTrue(saveData["last_state.created"]) {
			reverter.Add(func() {
				_ = networkRemoveInterfaceIfNeeded(d.state, d.effectiveParentName, d.inst, d.config["parent"], d.config["vlan"])
			})

			err := d.setupParentSysctls(d.effectiveParentName)
			if err != nil {
				return nil, err
			}
		}
	}

	if d.effectiveParentName != "" {
		err := d.checkIPAvailability(d.effectiveParentName)
		if err != nil {
			return nil, err
		}
	}

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

	reverter.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, nil, false)
	if err != nil {
		return nil, err
	}

	// Attempt to disable IPv6 router advertisement acceptance from instance.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Prevent source address spoofing by requiring a return path.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", saveData["host_name"]), "1")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Apply firewall rules for reverse path filtering of IPv4 and IPv6.
	err = d.state.Firewall.InstanceSetupRPFilter(d.inst.Project().Name, d.inst.Name(), d.name, saveData["host_name"])
	if err != nil {
		return nil, fmt.Errorf("Error setting up reverse path filter: %w", err)
	}

	// Perform host-side address configuration.
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		subnetSize := 32
		ipFamilyArg := ip.FamilyV4
		if keyPrefix == "ipv6" {
			subnetSize = 128
			ipFamilyArg = ip.FamilyV6
		}

		addresses := util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.address", keyPrefix)], ",", -1, true)

		// Add host-side gateway addresses.
		if len(addresses) > 0 {
			// Add gateway IPs to the host end of the veth pair. This ensures that liveness detection
			// of the gateways inside the instance work and ensure that traffic doesn't periodically
			// halt whilst ARP/NDP is re-detected (which is what happens with just neighbour proxies).
			addr := &ip.Addr{
				DevName: saveData["host_name"],
				Address: &net.IPNet{
					IP:   d.ipHostAddress(keyPrefix),
					Mask: net.CIDRMask(subnetSize, subnetSize),
				},
				Family: ipFamilyArg,
			}

			err = addr.Add()
			if err != nil {
				return nil, fmt.Errorf("Failed adding host gateway IP %q: %w", addr.Address, err)
			}

			// Enable IP forwarding on host_name.
			err = localUtil.SysctlSet(fmt.Sprintf("net/%s/conf/%s/forwarding", keyPrefix, saveData["host_name"]), "1")
			if err != nil {
				return nil, err
			}
		}

		getTables := func() []string {
			// New plural form – honour exactly what the user gives.
			v := d.config[fmt.Sprintf("%s.host_tables", keyPrefix)]
			if v != "" {
				return util.SplitNTrimSpace(v, ",", -1, true)
			}

			// Legacy – single key: include it plus 254.
			v = d.config[fmt.Sprintf("%s.host_table", keyPrefix)]
			if v != "" {
				if v == "254" {
					return []string{"254"} // user asked for main only
				}

				return []string{v, "254"} // custom + main
			}

			// Default – main only.
			return []string{"254"}
		}

		tables := getTables()

		// Perform per-address host-side configuration (static routes and neighbour proxy entries).
		for _, addrStr := range addresses {
			// Apply host-side static routes to main routing table or VRF.

			address := net.ParseIP(addrStr)
			if address == nil {
				return nil, fmt.Errorf("Invalid address %q", addrStr)
			}

			// If a VRF is set we still add a route into the VRF's own table (empty Table value).
			if d.config["vrf"] != "" {
				r := ip.Route{
					DevName: saveData["host_name"],
					Route: &net.IPNet{
						IP:   address,
						Mask: net.CIDRMask(subnetSize, subnetSize),
					},
					Table:  "",
					Family: ipFamilyArg,
					VRF:    d.config["vrf"],
				}

				err = r.Add()
				if err != nil {
					return nil, fmt.Errorf("Failed adding host route %q: %w", r.Route, err)
				}
			}

			// Add routes to all requested tables.
			for _, tbl := range tables {
				r := ip.Route{
					DevName: saveData["host_name"],
					Route: &net.IPNet{
						IP:   address,
						Mask: net.CIDRMask(subnetSize, subnetSize),
					},
					Table:  tbl,
					Family: ipFamilyArg,
				}

				err = r.Add()
				if err != nil {
					return nil, fmt.Errorf("Failed adding host route %q to table %q: %w", r.Route, r.Table, err)
				}
			}

			// If there is a parent interface, add neighbour proxy entry.
			if d.effectiveParentName != "" {
				np := ip.NeighProxy{
					DevName: d.effectiveParentName,
					Addr:    net.ParseIP(addrStr),
				}

				err = np.Add()
				if err != nil {
					return nil, fmt.Errorf("Failed adding neighbour proxy %q to %q: %w", np.Addr.String(), np.DevName, err)
				}

				reverter.Add(func() { _ = np.Delete() })
			}
		}

		if d.config[fmt.Sprintf("%s.routes", keyPrefix)] != "" {
			routes := util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.routes", keyPrefix)], ",", -1, true)

			if len(addresses) == 0 {
				return nil, fmt.Errorf("%s.routes requires %s.address to be set", keyPrefix, keyPrefix)
			}

			viaAddress := net.ParseIP(addresses[0])
			if viaAddress == nil {
				return nil, fmt.Errorf("Invalid address %q", addresses[0])
			}

			// Add routes
			for _, routeStr := range routes {
				route, err := ip.ParseIPNet(routeStr)
				if err != nil {
					return nil, fmt.Errorf("Invalid route %q: %w", routeStr, err)
				}
				// If a VRF is set we still add a route into the VRF's own table (empty Table value).
				if d.config["vrf"] != "" {
					r := ip.Route{
						DevName: saveData["host_name"],
						Route:   route,
						Table:   "",
						Family:  ipFamilyArg,
						Via:     viaAddress,
						VRF:     d.config["vrf"],
					}

					err = r.Add()
					if err != nil {
						return nil, fmt.Errorf("Failed adding route %q: %w", r.Route, err)
					}
				}

				// Add routes to all requested tables.
				for _, tbl := range tables {
					r := ip.Route{
						DevName: saveData["host_name"],
						Route:   route,
						Table:   tbl,
						Family:  ipFamilyArg,
						Via:     viaAddress,
					}

					err = r.Add()
					if err != nil {
						return nil, fmt.Errorf("Failed adding route %q to table %q: %w", r.Route, r.Table, err)
					}
				}
			}
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Perform instance NIC configuration.
	runConf := deviceConfig.RunConfig{}
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

	if d.inst.Type() == instancetype.Container {
		for _, keyPrefix := range []string{"ipv4", "ipv6"} {
			ipAddresses := util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.address", keyPrefix)], ",", -1, true)

			// Use a fixed address as the auto next-hop default gateway if using this IP family.
			if len(ipAddresses) > 0 && nicHasAutoGateway(d.config[fmt.Sprintf("%s.gateway", keyPrefix)]) {
				runConf.NetworkInterface = append(runConf.NetworkInterface,
					deviceConfig.RunConfigItem{Key: fmt.Sprintf("%s.gateway", keyPrefix), Value: d.ipHostAddress(keyPrefix).String()},
				)
			}

			for _, addrStr := range ipAddresses {
				// Add addresses to instance NIC.
				if keyPrefix == "ipv6" {
					runConf.NetworkInterface = append(runConf.NetworkInterface,
						deviceConfig.RunConfigItem{Key: "ipv6.address", Value: fmt.Sprintf("%s/128", addrStr)},
					)
				} else {
					// Specify the broadcast address as 0.0.0.0 as there is no broadcast address on
					// this link. This stops liblxc from trying to calculate a broadcast address
					// (and getting it wrong) which can prevent instances communicating with each other
					// using adjacent IP addresses.
					runConf.NetworkInterface = append(runConf.NetworkInterface,
						deviceConfig.RunConfigItem{Key: "ipv4.address", Value: fmt.Sprintf("%s/32 0.0.0.0", addrStr)},
					)
				}
			}
		}
	} else if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface, []deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
		}...)
	}

	reverter.Success()

	return &runConf, nil
}

// setupParentSysctls configures the required sysctls on the parent to allow l2proxy to work.
// Because of our policy not to modify sysctls on existing interfaces, this should only be called
// if we created the parent interface.
func (d *nicRouted) setupParentSysctls(parentName string) error {
	if d.config["ipv4.address"] != "" {
		// Set necessary sysctls for use with l2proxy parent in routed mode.
		ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", parentName)
		err := localUtil.SysctlSet(ipv4FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %w", ipv4FwdPath, err)
		}
	}

	if d.config["ipv6.address"] != "" {
		// Set necessary sysctls use with l2proxy parent in routed mode.
		ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", parentName)
		err := localUtil.SysctlSet(ipv6FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %w", ipv6FwdPath, err)
		}

		ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", parentName)
		err = localUtil.SysctlSet(ipv6ProxyNdpPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %w", ipv6ProxyNdpPath, err)
		}
	}

	return nil
}

// Update returns an error as most devices do not support live updates without being restarted.
func (d *nicRouted) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	v := d.volatileGet()

	// If instance is running, apply host side limits.
	if isRunning {
		err := d.validateEnvironment()
		if err != nil {
			return err
		}

		// Populate device config with volatile fields if needed.
		networkVethFillFromVolatile(d.config, v)

		// Apply host-side limits.
		err = networkSetupHostVethLimits(&d.deviceCommon, oldDevices[d.name], false)
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicRouted) Stop() (*deviceConfig.RunConfig, error) {
	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	err := networkClearHostVethLimits(&d.deviceCommon)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicRouted) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"last_state.created": "",
			"host_name":          "",
		})
	}()

	errs := []error{}

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["parent"] != "" {
		d.effectiveParentName = network.GetHostDevice(d.config["parent"], d.config["vlan"])
	}

	// Delete host-side interface.
	if network.InterfaceExists(d.config["host_name"]) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			errs = append(errs, fmt.Errorf("Failed to remove interface %q: %w", d.config["host_name"], err))
		}
	}

	// Delete IP neighbour proxy entries on the parent.
	if d.effectiveParentName != "" {
		for _, key := range []string{"ipv4.address", "ipv6.address"} {
			for _, addr := range util.SplitNTrimSpace(d.config[key], ",", -1, true) {
				neighProxy := &ip.NeighProxy{
					DevName: d.effectiveParentName,
					Addr:    net.ParseIP(addr),
				}

				_ = neighProxy.Delete()
			}
		}
	}

	// This will delete the parent interface if we created it for VLAN parent.
	if util.IsTrue(v["last_state.created"]) {
		err := networkRemoveInterfaceIfNeeded(d.state, d.effectiveParentName, d.inst, d.config["parent"], d.config["vlan"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Remove reverse path filters.
	err := d.state.Firewall.InstanceClearRPFilter(d.inst.Project().Name, d.inst.Name(), d.name)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}

func (d *nicRouted) ipHostAddress(ipFamily string) net.IP {
	key := fmt.Sprintf("%s.host_address", ipFamily)
	if d.config[key] != "" {
		return net.ParseIP(d.config[key])
	}

	return nicRoutedIPGateway[ipFamily]
}

func (d *nicRouted) isUniqueWithGatewayAutoMode(instConf instance.ConfigReader) error {
	instDevs := instConf.ExpandedDevices()
	for _, k := range []string{"ipv4.gateway", "ipv6.gateway"} {
		if d.config[k] != "auto" && d.config[k] != "" {
			continue // nothing to do as auto not being used.
		}

		// Check other routed NIC devices don't have auto set.
		for nicName, nicConfig := range instDevs {
			if nicName == d.name || nicConfig["nictype"] != "routed" {
				continue // Skip ourselves.
			}

			if nicConfig[k] == "auto" || nicConfig[k] == "" {
				return fmt.Errorf("Existing NIC %q already uses %q in auto mode", nicName, k)
			}
		}
	}

	return nil
}
