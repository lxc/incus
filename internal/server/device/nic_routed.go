package device

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/linux"
	"github.com/lxc/incus/v7/internal/server/apparmor"
	"github.com/lxc/incus/v7/internal/server/daemon"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/device/nictype"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/internal/server/network"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
	"github.com/lxc/incus/v7/shared/validate"
)

var nicRoutedIPGateway = map[string]net.IP{
	"ipv4": net.IPv4(169, 254, 0, 1),                                  // 169.254.0.1
	"ipv6": {0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}, // fe80::1
}

type nicRouted struct {
	deviceCommon
	effectiveParentName string
	network             network.Network   // Populated in validateConfig().
	assignedAddresses   map[string]string // Addresses assigned from the network, keyed by IP family.
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

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.ingress.burst", "limits.egress.burst", "limits.max.burst", "limits.ingress.bucket", "limits.egress.bucket", "limits.max.bucket", "limits.priority", "queue.discipline", "queue.discipline.attach", "connected"}
}

// validateConfig checks the supplied config for correctness.
func (d *nicRouted) validateConfig(instConf instance.ConfigReader, partialValidation bool) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	err := d.isUniqueWithGatewayAutoMode(instConf)
	if err != nil {
		return err
	}

	requiredFields := []string{}
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_routed, key=network)
		//
		// ---
		//  type: string
		//  shortdesc: The managed network to link the device to (instead of specifying the `nictype` directly)
		"network",

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

		// gendoc:generate(entity=devices, group=nic_routed, key=queue.discipline)
		//
		// ---
		//  type: string
		//  shortdesc: The queuing discipline to set on the NIC, applied to the rate limit's class when a limit is set
		"queue.discipline",

		// gendoc:generate(entity=devices, group=nic_routed, key=queue.discipline.attach)
		//
		// ---
		//  type: string
		//  default: `queue`
		//  shortdesc: Only for VMs: Whether to attach the queuing discipline to each transmit queue (`queue`) or to the interface root (`root`)
		"queue.discipline.attach",

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
		//  shortdesc: I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
		"limits.ingress",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.egress)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
		"limits.egress",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.max)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for both incoming and outgoing traffic (same as setting both limits.ingress and limits.egress)
		"limits.max",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.ingress.burst)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit in bit/s that incoming traffic may burst up to, requires `limits.ingress.bucket`
		"limits.ingress.burst",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.egress.burst)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit in bit/s that outgoing traffic may burst up to, requires `limits.egress.bucket`
		"limits.egress.burst",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.max.burst)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit in bit/s that both incoming and outgoing traffic may burst up to (same as setting both `limits.ingress.burst` and `limits.egress.burst`)
		"limits.max.burst",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.ingress.bucket)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Amount of data in bit that incoming traffic may send in excess of `limits.ingress`, requires `limits.ingress.burst`
		"limits.ingress.bucket",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.egress.bucket)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Amount of data in bit that outgoing traffic may send in excess of `limits.egress`, requires `limits.egress.burst`
		"limits.egress.bucket",

		// gendoc:generate(entity=devices, group=nic_routed, key=limits.max.bucket)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Amount of data in bit that traffic may send in excess of the sustained limit (same as setting both `limits.ingress.bucket` and `limits.egress.bucket`)
		"limits.max.bucket",

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

		// gendoc:generate(entity=devices, group=nic_routed, key=attached)
		//
		// ---
		//  type: bool
		//  default: `true`
		//  required: no
		//  shortdesc: Whether the NIC is plugged in or not
		"attached",

		// gendoc:generate(entity=devices, group=nic_routed, key=connected)
		//
		// ---
		//  type: bool
		//  default: `true`
		//  required: no
		//  shortdesc: Whether the NIC is connected to the host network
		"connected",
	}

	checkWithManagedNetwork := func(n network.Network) error {
		if n.Status() != api.NetworkStatusCreated {
			return errors.New("Specified network is not fully created")
		}

		if n.Type() != "routed" {
			return errors.New("Specified network must be of type routed")
		}

		for _, family := range []string{"ipv4", "ipv6"} {
			key := fmt.Sprintf("%s.address", family)
			// One address per family for now, as address assignment, conflict checks and DHCP handle a single one.
			addresses := util.SplitNTrimSpace(d.config[key], ",", -1, true)
			if len(addresses) > 1 {
				return fmt.Errorf("Only one address can be set in %q when using a managed network", key)
			}

			for _, addrStr := range addresses {
				addr := net.ParseIP(addrStr)
				if addr == nil {
					return fmt.Errorf("Invalid address %q", addrStr)
				}

				subnet := routedNetworkSubnet(n, family, addr)
				if subnet == nil {
					return fmt.Errorf("Device IP address %q not within network %q subnets", addrStr, n.Name())
				}

				// The subnet-router anycast address is used by the host for router advertisements.
				if family == "ipv6" && addr.Equal(subnet.IP) {
					return fmt.Errorf("Device IP address %q is reserved as the subnet-router anycast address", addrStr)
				}
			}
		}

		return nil
	}

	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "gvrp", "vrf", "ipv4.host_address", "ipv6.host_address", "ipv4.host_table", "ipv6.host_table", "ipv4.host_tables", "ipv6.host_tables", "ipv4.routes", "ipv6.routes"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// api.ProjectDefaultName is used here because managed routed networks are in the default project.
		d.network, err = network.LoadByName(d.state, api.ProjectDefaultName, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		// Use the addresses assigned on an earlier start unless addresses are configured or the network's subnets changed.
		d.assignedAddresses = map[string]string{}
		for _, family := range []string{"ipv4", "ipv6"} {
			key := fmt.Sprintf("%s.address", family)
			assigned := instConf.ExpandedConfig()[fmt.Sprintf("volatile.%s.%s", d.name, key)]

			if d.config[key] == "" && routedNetworkSubnet(d.network, family, net.ParseIP(assigned)) != nil {
				d.config[key] = assigned
				d.assignedAddresses[family] = assigned
			}
		}

		err = checkWithManagedNetwork(d.network)
		if err != nil {
			return err
		}

		netConfig := d.network.Config()
		d.config["parent"] = netConfig["parent"]

		for _, key := range []string{"mtu", "vlan", "gvrp", "vrf", "ipv4.gateway", "ipv6.gateway", "ipv4.host_address", "ipv6.host_address", "ipv4.host_tables", "ipv6.host_tables"} {
			if d.config[key] == "" && netConfig[key] != "" {
				d.config[key] = netConfig[key]
			}
		}
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

	err = nicValidateBurstLimits(d.config, true)
	if err != nil {
		return err
	}

	err = nicValidateQdisc(d.config, instConf.Type())
	if err != nil {
		return err
	}

	// Detect duplicate IPs in config.
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		ips := make(map[string]struct{})

		if d.config[key] != "" {
			for addr := range strings.SplitSeq(d.config[key], ",") {
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

// PreStartCheck checks the managed parent network is available (if relevant).
func (d *nicRouted) PreStartCheck() error {
	if d.network == nil {
		return nil
	}

	if d.network.LocalStatus() == api.NetworkStatusUnavailable {
		return api.StatusErrorf(503, "Network %q unavailable on this server", d.network.Name())
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

			// net.ipv6.conf.all.proxy_ndp=1 is needed otherwise unicast neighbor solicitations are .
			// rejected This causes periodic latency spikes every 15-20s as the neighbor has to resort
			// to using multicast NDP resolution and expires the previous neighbor entry.
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

// checkIPAvailability checks using ARP and NDP neighbor probes whether any of the NIC's IPs are already in use.
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
	// Ignore detached NICs.
	if !util.IsTrueOrEmpty(d.config["attached"]) {
		return nil, nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	err = d.assignAddresses()
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
		err = d.generateAndPersistHostName(saveData, "veth")
		if err != nil {
			return nil, err
		}

		peerName, mtu, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		err = d.generateAndPersistHostName(saveData, "tap")
		if err != nil {
			return nil, err
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
			// halt whilst ARP/NDP is re-detected (which is what happens with just neighbor proxies).
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

		// Add neighbor proxy entries on the parent interface.
		if d.effectiveParentName != "" {
			for _, addrStr := range addresses {
				address := net.ParseIP(addrStr)
				if address == nil {
					return nil, fmt.Errorf("Invalid address %q", addrStr)
				}

				np := ip.NeighProxy{
					DevName: d.neighProxyDevice(address),
					Addr:    address,
				}

				err = np.Add()
				if err != nil {
					return nil, fmt.Errorf("Failed adding neighbor proxy %q to %q: %w", np.Addr.String(), np.DevName, err)
				}

				reverter.Add(func() { _ = np.Delete() })
			}
		}
	}

	if d.dhcpEnabled("ipv6") {
		err = d.setupDHCPv6Host(saveData["host_name"])
		if err != nil {
			return nil, fmt.Errorf("Failed preparing %q for DHCPv6: %w", saveData["host_name"], err)
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Perform instance NIC configuration.
	runConf := deviceConfig.RunConfig{}

	// The host routes attract traffic, so a VM only gets them once it runs (after a live migration's hand-over).
	if d.inst.Type() == instancetype.VM {
		runConf.PostHooks = []func() error{d.setupHostRoutes}

		if d.dhcpEnabled("ipv4") || d.dhcpEnabled("ipv6") {
			runConf.PostHooks = append(runConf.PostHooks, func() error { return d.startDHCP(saveData["host_name"], mtu) })
		}
	} else {
		err = d.setupHostRoutes()
		if err != nil {
			return nil, err
		}
	}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
		{Key: "connected", Value: d.config["connected"]},
	}

	if d.config["io.bus"] == "usb" {
		runConf.UseUSBBus = true
	}

	if d.inst.Type() == instancetype.Container {
		for _, keyPrefix := range []string{"ipv4", "ipv6"} {
			ipAddresses := util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.address", keyPrefix)], ",", -1, true)

			// Use a fixed address as the auto next-hop default gateway if using this IP family.
			if len(ipAddresses) > 0 && nicHasAutoGateway(d.config[fmt.Sprintf("%s.gateway", keyPrefix)]) {
				runConf.NetworkInterface = append(
					runConf.NetworkInterface,
					deviceConfig.RunConfigItem{Key: fmt.Sprintf("%s.gateway", keyPrefix), Value: d.ipHostAddress(keyPrefix).String()},
				)
			}

			for _, addrStr := range ipAddresses {
				// Add addresses to instance NIC.
				if keyPrefix == "ipv6" {
					runConf.NetworkInterface = append(
						runConf.NetworkInterface,
						deviceConfig.RunConfigItem{Key: "ipv6.address", Value: fmt.Sprintf("%s/128", addrStr)},
					)
				} else {
					// Specify the broadcast address as 0.0.0.0 as there is no broadcast address on
					// this link. This stops liblxc from trying to calculate a broadcast address
					// (and getting it wrong) which can prevent instances communicating with each other
					// using adjacent IP addresses.
					runConf.NetworkInterface = append(
						runConf.NetworkInterface,
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

// hostTables returns the routing tables the host routes of an IP family go into.
func (d *nicRouted) hostTables(keyPrefix string) []string {
	// New plural form, honor exactly what the user gives.
	v := d.config[fmt.Sprintf("%s.host_tables", keyPrefix)]
	if v != "" {
		return util.SplitNTrimSpace(v, ",", -1, true)
	}

	// Legacy single key, include it plus 254.
	v = d.config[fmt.Sprintf("%s.host_table", keyPrefix)]
	if v != "" {
		if v == "254" {
			return []string{"254"} // user asked for main only
		}

		return []string{v, "254"} // custom + main
	}

	// Default, main only.
	return []string{"254"}
}

// setupHostRoutes adds the host routes for the instance's addresses and its static routes.
func (d *nicRouted) setupHostRoutes() error {
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		subnetSize := 32
		ipFamilyArg := ip.FamilyV4
		if keyPrefix == "ipv6" {
			subnetSize = 128
			ipFamilyArg = ip.FamilyV6
		}

		tables := d.hostTables(keyPrefix)
		addresses := util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.address", keyPrefix)], ",", -1, true)

		for _, addrStr := range addresses {
			address := net.ParseIP(addrStr)
			if address == nil {
				return fmt.Errorf("Invalid address %q", addrStr)
			}

			route := &net.IPNet{
				IP:   address,
				Mask: net.CIDRMask(subnetSize, subnetSize),
			}

			err := d.addHostRoute(route, nil, ipFamilyArg, tables)
			if err != nil {
				return err
			}
		}

		if d.config[fmt.Sprintf("%s.routes", keyPrefix)] == "" {
			continue
		}

		if len(addresses) == 0 {
			return fmt.Errorf("%s.routes requires %s.address to be set", keyPrefix, keyPrefix)
		}

		viaAddress := net.ParseIP(addresses[0])
		if viaAddress == nil {
			return fmt.Errorf("Invalid address %q", addresses[0])
		}

		for _, routeStr := range util.SplitNTrimSpace(d.config[fmt.Sprintf("%s.routes", keyPrefix)], ",", -1, true) {
			route, err := ip.ParseIPNet(routeStr)
			if err != nil {
				return fmt.Errorf("Invalid route %q: %w", routeStr, err)
			}

			err = d.addHostRoute(route, viaAddress, ipFamilyArg, tables)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// addHostRoute adds a route through the host interface to the VRF and to every requested table.
// Existing routes are replaced so a leftover or learned route can't block the start.
func (d *nicRouted) addHostRoute(route *net.IPNet, via net.IP, family ip.Family, tables []string) error {
	// If a VRF is set we still add a route into the VRF's own table (empty Table value).
	if d.config["vrf"] != "" {
		r := ip.Route{
			DevName: d.config["host_name"],
			Route:   route,
			Table:   "",
			Family:  family,
			Via:     via,
			VRF:     d.config["vrf"],
		}

		err := r.Replace()
		if err != nil {
			return fmt.Errorf("Failed adding host route %q: %w", r.Route, err)
		}
	}

	for _, tbl := range tables {
		r := ip.Route{
			DevName: d.config["host_name"],
			Route:   route,
			Table:   tbl,
			Family:  family,
			Via:     via,
		}

		err := r.Replace()
		if err != nil {
			return fmt.Errorf("Failed adding host route %q to table %q: %w", r.Route, r.Table, err)
		}
	}

	return nil
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

		return d.setNICLink()
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

	err := d.stopDHCP()
	if err != nil {
		errs = append(errs, err)
	}

	// Delete host-side interface.
	if network.InterfaceExists(d.config["host_name"]) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			errs = append(errs, fmt.Errorf("Failed to remove interface %q: %w", d.config["host_name"], err))
		}
	}

	// Delete IP neighbor proxy entries on the parent.
	if d.effectiveParentName != "" {
		for _, key := range []string{"ipv4.address", "ipv6.address"} {
			for _, addr := range util.SplitNTrimSpace(d.config[key], ",", -1, true) {
				address := net.ParseIP(addr)

				neighProxy := &ip.NeighProxy{
					DevName: d.neighProxyDevice(address),
					Addr:    address,
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
	err = d.state.Firewall.InstanceClearRPFilter(d.inst.Project().Name, d.inst.Name(), d.name)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}

// neighProxyDevice returns the interface on which the address is on-link, preferring the parent.
func (d *nicRouted) neighProxyDevice(addr net.IP) string {
	onLink := func(devName string) bool {
		iface, err := net.InterfaceByName(devName)
		if err != nil {
			return false
		}

		addrs, err := iface.Addrs()
		if err != nil {
			return false
		}

		for _, ifaceAddr := range addrs {
			ipNet, ok := ifaceAddr.(*net.IPNet)
			if ok && ipNet.Contains(addr) {
				return true
			}
		}

		return false
	}

	if onLink(d.effectiveParentName) {
		return d.effectiveParentName
	}

	// Look for an upper interface carrying the address's subnet.
	entries, err := os.ReadDir(fmt.Sprintf("/sys/class/net/%s", d.effectiveParentName))
	if err == nil {
		for _, entry := range entries {
			devName, ok := strings.CutPrefix(entry.Name(), "upper_")
			if !ok {
				continue
			}

			if onLink(devName) {
				return devName
			}
		}
	}

	return d.effectiveParentName
}

func (d *nicRouted) ipHostAddress(ipFamily string) net.IP {
	key := fmt.Sprintf("%s.host_address", ipFamily)
	if d.config[key] != "" {
		return net.ParseIP(d.config[key])
	}

	return nicRoutedIPGateway[ipFamily]
}

// dhcpEnabled returns whether the NIC's addresses of an IP family are handed out over DHCP (containers are configured directly).
func (d *nicRouted) dhcpEnabled(keyPrefix string) bool {
	if d.network == nil || d.inst.Type() != instancetype.VM {
		return false
	}

	if util.IsFalse(d.network.Config()[fmt.Sprintf("%s.dhcp", keyPrefix)]) || d.config[fmt.Sprintf("%s.address", keyPrefix)] == "" {
		return false
	}

	if !nicHasAutoGateway(d.config[fmt.Sprintf("%s.gateway", keyPrefix)]) {
		return false
	}

	if keyPrefix == "ipv6" {
		// Router advertisements must be sent from a link-local address.
		return d.dhcpSubnet("ipv6") != nil && d.ipHostAddress("ipv6").IsLinkLocalUnicast()
	}

	return d.dhcpSubnet("ipv4") != nil
}

// dhcpSubnet returns the network subnet containing the NIC's address of an IP family.
func (d *nicRouted) dhcpSubnet(keyPrefix string) *net.IPNet {
	return routedNetworkSubnet(d.network, keyPrefix, net.ParseIP(d.config[fmt.Sprintf("%s.address", keyPrefix)]))
}

// setupDHCPv6Host prepares the host interface for DHCPv6, before it has carrier so no link-local address is generated.
func (d *nicRouted) setupDHCPv6Host(hostName string) error {
	// Keep the gateway address as the only router address, so that it is the same on all members.
	err := localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/addr_gen_mode", hostName), "1")
	if err != nil {
		return err
	}

	// Allow resolving the instance's link-local address (as the kernel would), used by dnsmasq to identify the client.
	_, linkLocal, _ := net.ParseCIDR("fe80::/64")
	r := ip.Route{DevName: hostName, Route: linkLocal, Family: ip.FamilyV6, VRF: d.config["vrf"]}
	err = r.Replace()
	if err != nil {
		return err
	}

	// Router advertisements need the prefix on the interface, so add the subnet-router anycast address.
	subnet := d.dhcpSubnet("ipv6")
	addr := ip.Addr{
		DevName: hostName,
		Address: subnet,
		Family:  ip.FamilyV6,
		Flags:   unix.IFA_F_NOPREFIXROUTE | unix.IFA_F_NODAD,
	}

	return addr.Add()
}

// dhcpPIDPath returns the path of the NIC's DHCP server PID file.
func (d *nicRouted) dhcpPIDPath() string {
	return filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("dnsmasq.%s.pid", linux.PathNameEncode(d.name)))
}

// startDHCP runs dnsmasq to hand out the NIC's addresses as a /32 and /128 behind the host-side gateways.
func (d *nicRouted) startDHCP(hostName string, mtu uint32) error {
	_, err := exec.LookPath("dnsmasq")
	if err != nil {
		return errors.New("dnsmasq is required for DHCP on routed networks")
	}

	err = d.stopDHCP()
	if err != nil {
		return err
	}

	args := []string{
		"--keep-in-foreground", "--port=0", "--bind-interfaces", "--except-interface=lo",
		fmt.Sprintf("--interface=%s", hostName),
		"--pid-file=", "--no-ping", "--leasefile-ro", "--dhcp-authoritative", "--dhcp-rapid-commit",
	}

	hostLine := d.config["hwaddr"]
	var nameserversV4, nameserversV6 []string
	for _, s := range util.SplitNTrimSpace(d.network.Config()["dns.nameservers"], ",", -1, true) {
		if net.ParseIP(s).To4() != nil {
			nameserversV4 = append(nameserversV4, s)
		} else {
			nameserversV6 = append(nameserversV6, fmt.Sprintf("[%s]", s))
		}
	}

	if d.dhcpEnabled("ipv4") {
		// The host interface only carries the gateway address, so the subnet is served as a shared network.
		subnet := d.dhcpSubnet("ipv4")
		gateway := d.ipHostAddress("ipv4").String()
		hostLine += "," + d.config["ipv4.address"]

		args = append(args,
			fmt.Sprintf("--shared-network=%s,%s", hostName, subnet.IP.String()),
			fmt.Sprintf("--dhcp-range=%s,static,%s,1h", subnet.IP.String(), net.IP(subnet.Mask).String()),
			"--dhcp-option-force=1,255.255.255.255",
			fmt.Sprintf("--dhcp-option-force=3,%s", gateway),
			fmt.Sprintf("--dhcp-option-force=121,%s/32,0.0.0.0,0.0.0.0/0,%s", gateway, gateway),
		)

		if mtu > 0 {
			args = append(args, fmt.Sprintf("--dhcp-option-force=26,%d", mtu))
		}

		if len(nameserversV4) > 0 {
			args = append(args, fmt.Sprintf("--dhcp-option-force=6,%s", strings.Join(nameserversV4, ",")))
		}
	}

	if d.dhcpEnabled("ipv6") {
		// Advertise the subnet off-link and without SLAAC, so that all traffic goes through the gateway.
		subnet := d.dhcpSubnet("ipv6")
		prefixLen, _ := subnet.Mask.Size()
		hostLine += fmt.Sprintf(",[%s]", d.config["ipv6.address"])

		raParam := fmt.Sprintf("--ra-param=%s,60,1800", hostName)
		if mtu > 0 {
			raParam = fmt.Sprintf("--ra-param=%s,mtu:%d,60,1800", hostName, mtu)
		}

		args = append(args,
			"--enable-ra", raParam,
			fmt.Sprintf("--dhcp-range=%s,static,off-link,%d,1h", subnet.IP.String(), prefixLen),
		)

		if len(nameserversV6) > 0 {
			args = append(args, fmt.Sprintf("--dhcp-option-force=option6:dns-server,%s", strings.Join(nameserversV6, ",")))
		}
	}

	args = append(args, fmt.Sprintf("--dhcp-host=%s", hostLine))

	if !daemon.Debug {
		args = append(args, "--quiet-dhcp", "--quiet-dhcp6", "--quiet-ra")
	}

	if d.state.OS.UnprivGroup != "" {
		args = append(args, "-g", d.state.OS.UnprivGroup)
	}

	logPath := filepath.Join(d.inst.LogPath(), fmt.Sprintf("dnsmasq.%s.%s.log", d.network.Name(), linux.PathNameEncode(d.name)))
	p, err := subprocess.NewProcess("dnsmasq", args, "", logPath)
	if err != nil {
		return fmt.Errorf("Failed to create DHCP server subprocess: %w", err)
	}

	p.SetApparmor(apparmor.DnsmasqProfileName(d.network))

	err = p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("Failed to run DHCP server: dnsmasq %s: %w", strings.Join(args, " "), err)
	}

	// Check dnsmasq started OK.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = p.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		stderr, _ := os.ReadFile(logPath)
		return fmt.Errorf("The DHCP server exited prematurely: %w (%q)", err, strings.TrimSpace(string(stderr)))
	}

	err = p.Save(d.dhcpPIDPath())
	if err != nil {
		_ = p.Stop()
		return fmt.Errorf("Failed to save DHCP server subprocess details: %w", err)
	}

	return nil
}

// stopDHCP stops the NIC's DHCP server if running.
func (d *nicRouted) stopDHCP() error {
	pidPath := d.dhcpPIDPath()
	if !util.PathExists(pidPath) {
		return nil
	}

	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read DHCP server PID file: %w", err)
	}

	err = p.Stop()
	if err != nil && !errors.Is(err, subprocess.ErrNotRunning) {
		return fmt.Errorf("Unable to stop DHCP server: %w", err)
	}

	err = os.Remove(pidPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// assignAddresses assigns free addresses from the network's subnets to the IP families without a configured address.
func (d *nicRouted) assignAddresses() error {
	if d.network == nil {
		return nil
	}

	conflicts, err := d.addressConflicts()
	if err != nil {
		return err
	}

	// Configured addresses must be unique, assigned ones are replaced if another NIC configured them since.
	for _, family := range []string{"ipv4", "ipv6"} {
		key := fmt.Sprintf("%s.address", family)
		addr, err := netip.ParseAddr(d.config[key])
		if err != nil || !conflicts[addr] {
			continue
		}

		if d.assignedAddresses[family] == "" {
			return api.StatusErrorf(http.StatusConflict, "IP address %q already used by another NIC on network %q", d.config[key], d.network.Name())
		}

		delete(d.assignedAddresses, family)
		d.config[key] = ""
	}

	families := []string{}
	for _, family := range []string{"ipv4", "ipv6"} {
		if d.config[fmt.Sprintf("%s.address", family)] == "" && d.network.Config()[fmt.Sprintf("%s.subnets", family)] != "" {
			families = append(families, family)
		}
	}

	if len(families) == 0 {
		return nil
	}

	assigned := map[string]string{}
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Collect the addresses of all other NICs on the network and of unmanaged routed NICs.
		used := map[netip.Addr]bool{}
		err := tx.InstanceList(ctx, func(inst db.InstanceArgs, _ api.Project) error {
			for _, addr := range d.peerAddresses(&inst, families, false) {
				used[addr] = true
			}

			return nil
		})
		if err != nil {
			return err
		}

		changes := map[string]string{}
		for _, family := range families {
			hostAddr, ok := netip.AddrFromSlice(d.ipHostAddress(family))
			if ok {
				used[hostAddr.Unmap()] = true
			}

			subnets := []netip.Prefix{}
			for _, subnetStr := range util.SplitNTrimSpace(d.network.Config()[fmt.Sprintf("%s.subnets", family)], ",", -1, true) {
				subnet, err := netip.ParsePrefix(subnetStr)
				if err != nil {
					return err
				}

				subnets = append(subnets, subnet)
			}

			addr, err := routedFreeAddress(subnets, used)
			if err != nil {
				return fmt.Errorf("Failed assigning %s address from network %q: %w", family, d.network.Name(), err)
			}

			assigned[fmt.Sprintf("%s.address", family)] = addr.String()
			changes[fmt.Sprintf("volatile.%s.%s.address", d.name, family)] = addr.String()
		}

		// Store the addresses in the same transaction, so that concurrent starts can't pick them too.
		return tx.UpdateInstanceConfig(d.inst.ID(), changes)
	})
	if err != nil {
		return err
	}

	// Also update the instance's in-memory config.
	err = d.volatileSet(assigned)
	if err != nil {
		return err
	}

	for key, addr := range assigned {
		d.config[key] = addr
	}

	return nil
}

// addressConflicts returns the NIC's addresses that are also used by another NIC on the same network.
func (d *nicRouted) addressConflicts() (map[netip.Addr]bool, error) {
	ours := map[netip.Addr]bool{}
	addresses := []string{}
	for _, family := range []string{"ipv4", "ipv6"} {
		addrStr := d.config[fmt.Sprintf("%s.address", family)]
		addr, err := netip.ParseAddr(addrStr)
		if err != nil {
			continue
		}

		ours[addr] = true
		addresses = append(addresses, addr.String(), strings.ToLower(addrStr))
	}

	conflicts := map[netip.Addr]bool{}
	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		candidateIDs, err := tx.GetNICConflictCandidateIDs(ctx, "", "", "", addresses)
		if err != nil || len(candidateIDs) == 0 {
			return err
		}

		filters := make([]cluster.InstanceFilter, 0, len(candidateIDs))
		for _, id := range candidateIDs {
			filters = append(filters, cluster.InstanceFilter{ID: &id})
		}

		return tx.InstanceList(ctx, func(inst db.InstanceArgs, _ api.Project) error {
			for _, addr := range d.peerAddresses(&inst, []string{"ipv4", "ipv6"}, true) {
				if ours[addr] {
					conflicts[addr] = true
				}
			}

			return nil
		}, filters...)
	})
	if err != nil {
		return nil, err
	}

	return conflicts, nil
}

// peerAddresses returns the configured or assigned addresses of an instance's other NICs on the same network and,
// unless sameNetwork is set, the addresses of its unmanaged routed NICs.
func (d *nicRouted) peerAddresses(inst *db.InstanceArgs, families []string, sameNetwork bool) []netip.Addr {
	addresses := []netip.Addr{}
	for devName, devConfig := range db.ExpandInstanceDevices(inst.Devices.Clone(), inst.Profiles) {
		if devConfig["type"] != "nic" {
			continue
		}

		if devConfig["network"] != d.config["network"] && (sameNetwork || devConfig["nictype"] != "routed") {
			continue
		}

		// Skip ourselves, also when a temporary copy of the instance exists during migrations.
		if instance.IsSameLogicalInstance(d.inst, inst) && devName == d.name {
			continue
		}

		for _, family := range families {
			key := fmt.Sprintf("%s.address", family)
			addrStrs := util.SplitNTrimSpace(devConfig[key], ",", -1, true)
			if len(addrStrs) == 0 {
				addrStrs = []string{inst.Config[fmt.Sprintf("volatile.%s.%s", devName, key)]}
			}

			for _, addrStr := range addrStrs {
				addr, err := netip.ParseAddr(addrStr)
				if err == nil {
					addresses = append(addresses, addr)
				}
			}
		}
	}

	return addresses
}

// routedFreeAddress returns the first unused address of the subnets, skipping IPv4 network and broadcast and IPv6 subnet-router anycast addresses.
func routedFreeAddress(subnets []netip.Prefix, used map[netip.Addr]bool) (netip.Addr, error) {
	for _, subnet := range subnets {
		subnet = subnet.Masked()

		// IPv4 /31 and /32 subnets have no network and broadcast addresses.
		hasNetworkAddress := subnet.Addr().Is6() || subnet.Bits() < 31

		addr := subnet.Addr()
		if hasNetworkAddress {
			addr = addr.Next()
		}

		for ; subnet.Contains(addr); addr = addr.Next() {
			if addr.Is4() && hasNetworkAddress && !subnet.Contains(addr.Next()) {
				break
			}

			if !used[addr] {
				return addr, nil
			}
		}
	}

	return netip.Addr{}, errors.New("No free address left")
}

// routedNetworkSubnet returns the subnet of a routed network containing the address, or nil if there is none.
func routedNetworkSubnet(n network.Network, keyPrefix string, addr net.IP) *net.IPNet {
	for _, subnetStr := range util.SplitNTrimSpace(n.Config()[fmt.Sprintf("%s.subnets", keyPrefix)], ",", -1, true) {
		_, subnet, err := net.ParseCIDR(subnetStr)
		if err == nil && subnet.Contains(addr) {
			return subnet
		}
	}

	return nil
}

func (d *nicRouted) isUniqueWithGatewayAutoMode(instConf instance.ConfigReader) error {
	instDevs := instConf.ExpandedDevices()
	for _, k := range []string{"ipv4.gateway", "ipv6.gateway"} {
		if d.config[k] != "auto" && d.config[k] != "" {
			continue // nothing to do as auto not being used.
		}

		// Check other routed NIC devices don't have auto set.
		for nicName, nicConfig := range instDevs {
			if nicName == d.name {
				continue // Skip ourselves.
			}

			nicType, err := nictype.NICType(d.state, instConf.Project().Name, nicConfig)
			if err != nil || nicType != "routed" {
				continue
			}

			if nicConfig[k] == "auto" || nicConfig[k] == "" {
				return fmt.Errorf("Existing NIC %q already uses %q in auto mode", nicName, k)
			}
		}
	}

	return nil
}
