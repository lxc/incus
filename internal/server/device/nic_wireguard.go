package device

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// nicWireguard represents a WireGuard network interface device.
// It embeds nicRouted to inherit all the routed functionality.
type nicWireguard struct {
	nicRouted
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *nicWireguard) CanHotPlug() bool {
	return true
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicWireguard) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicWireguard)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.priority"}
}

// validateConfig checks the supplied config for correctness.
func (d *nicWireguard) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	err := d.isUniqueWithGatewayAutoMode(instConf)
	if err != nil {
		return err
	}

	requiredFields := []string{}
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_wireguard, key=name)
		//
		// ---
		//  type: string
		//  default: kernel assigned
		//  shortdesc: The name of the interface inside the instance
		"name",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=network)
		//
		// ---
		//  type: string
		//  required: yes
		//  shortdesc: The managed WireGuard network to link the device to
		"network",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=mtu)
		//
		// ---
		//  type: integer
		//  default: parent MTU
		//  shortdesc: The Maximum Transmit Unit (MTU) of the new interface
		"mtu",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=hwaddr)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  shortdesc: The MAC address of the new interface
		"hwaddr",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=host_name)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  shortdesc: The name of the interface on the host
		"host_name",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=limits.ingress)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
		"limits.ingress",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=limits.egress)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
		"limits.egress",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=limits.max)
		//
		// ---
		//  type: string
		//  shortdesc: I/O limit in bit/s for both incoming and outgoing traffic (same as setting both limits.ingress and limits.egress)
		"limits.max",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=limits.priority)
		//
		// ---
		//  type: integer
		//  shortdesc: The priority for outgoing traffic, to be used by the kernel queuing discipline to prioritize network packets
		"limits.priority",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv4.gateway)
		//
		// ---
		//  type: string
		//  default: auto
		//  shortdesc: Whether to add an automatic default IPv4 gateway (can be `auto` or `none`)
		"ipv4.gateway",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv6.gateway)
		//
		// ---
		//  type: string
		//  default: auto
		//  shortdesc: Whether to add an automatic default IPv6 gateway (can be `auto` or `none`)
		"ipv6.gateway",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv4.routes)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-delimited list of IPv4 static routes to add on host to NIC
		"ipv4.routes",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv6.routes)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-delimited list of IPv6 static routes to add on host to NIC
		"ipv6.routes",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv4.neighbor_probe)
		//
		// ---
		//  type: bool
		//  default: true
		//  shortdesc: Whether to probe the parent network for IPv4 address availability using ARP
		"ipv4.neighbor_probe",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv6.neighbor_probe)
		//
		// ---
		//  type: bool
		//  default: true
		//  shortdesc: Whether to probe the parent network for IPv6 address availability using NDP
		"ipv6.neighbor_probe",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv4.host_tables)
		//
		// ---
		//  type: string
		//  default: 254
		//  shortdesc: Comma-delimited list of routing tables IDs to add IPv4 static routes to
		"ipv4.host_tables",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=ipv6.host_tables)
		//
		// ---
		//  type: string
		//  default: 254
		//  shortdesc: Comma-delimited list of routing tables IDs to add IPv6 static routes to
		"ipv6.host_tables",

		// gendoc:generate(entity=devices, group=nic_wireguard, key=vrf)
		//
		// ---
		//  type: string
		//  shortdesc: The VRF on the host in which the host-side interface and routes are created
		"vrf",
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	// Override ipv4.address and ipv6.address to support lists
	rules["ipv4.address"] = validate.Optional(validate.IsListOf(validate.IsNetworkAddressV4))
	rules["ipv6.address"] = validate.Optional(validate.IsListOf(validate.IsNetworkAddressV6))

	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	// Detect duplicate IPs in config.
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		ips := make(map[string]struct{})

		if d.config[key] != "" {
			for _, addr := range util.SplitNTrimSpace(d.config[key], ",", -1, true) {
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

	// Network property is required for WireGuard
	if d.config["network"] == "" {
		return errors.New("Network property is required for WireGuard NIC")
	}

	// Translate device's project name into a network project name.
	networkProjectName, _, err := project.NetworkProject(d.state.DB.Cluster, instConf.Project().Name)
	if err != nil {
		return fmt.Errorf("Failed to translate device project into network project: %w", err)
	}

	// Load the network
	wgNet, err := network.LoadByName(d.state, networkProjectName, d.config["network"])
	if err != nil {
		return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
	}

	if wgNet.Status() != api.NetworkStatusCreated {
		return errors.New("Specified network is not fully created")
	}

	if wgNet.Type() != "wireguard" {
		return fmt.Errorf("Specified network must be of type wireguard, got %q", wgNet.Type())
	}

	netConfig := wgNet.Config()

	// Get WireGuard interface name (from config or network name)
	ifaceName := netConfig["interface"]
	if ifaceName == "" {
		ifaceName = d.config["network"]
	}

	// Set parent to the WireGuard interface name (for routed NIC functionality)
	d.config["parent"] = ifaceName

	// Generate IP address from network's address range if not already set
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		addrKey := fmt.Sprintf("%s.address", keyPrefix)
		netAddrKey := fmt.Sprintf("%s.address", keyPrefix)

		// Only generate IP if not already set
		if d.config[addrKey] == "" && netConfig[netAddrKey] != "" {
			// Parse the network's address to get subnet (handle multiple addresses)
			addresses := util.SplitNTrimSpace(netConfig[netAddrKey], ",", -1, true)
			if len(addresses) > 0 {
				// Use the first address from the network's address list
				networkIP, subnet, err := net.ParseCIDR(addresses[0])
				if err != nil {
					return fmt.Errorf("Failed to parse network address %q: %w", addresses[0], err)
				}

				// Generate a random IP from the subnet
				// Avoid the network address and the WireGuard interface's own IP
				randomIP, err := network.GenerateRandomIPFromSubnet(subnet, networkIP)
				if err != nil {
					return fmt.Errorf("Failed to generate IP address from network subnet: %w", err)
				}

				d.config[addrKey] = randomIP.String()
			}
		}
	}

	return nil
}
