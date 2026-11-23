package network

import (
	"fmt"
	"slices"

	"github.com/lxc/incus/v7/internal/server/apparmor"
	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
	"github.com/lxc/incus/v7/shared/validate"
)

// routed represents a managed routed NIC profile network.
type routed struct {
	common
}

// DBType returns the network type DB ID.
func (n *routed) DBType() db.NetworkType {
	return db.NetworkTypeRouted
}

// Validate network config.
func (n *routed) Validate(config map[string]string, clientType request.ClientType) error {
	rules := map[string]func(value string) error{
		// gendoc:generate(entity=network_routed, group=common, key=parent)
		//
		// ---
		//  type: string
		//  shortdesc: Existing interface to use for neighbor proxy entries
		"parent": validate.Optional(validate.IsInterfaceName),

		// gendoc:generate(entity=network_routed, group=common, key=mtu)
		//
		// ---
		//  type: integer
		//  shortdesc: The MTU inherited by routed NICs linked to this network
		"mtu": validate.Optional(validate.IsNetworkMTU),

		// gendoc:generate(entity=network_routed, group=common, key=vlan)
		//
		// ---
		//  type: integer
		//  shortdesc: The VLAN ID inherited by routed NICs linked to this network
		"vlan": validate.Optional(validate.IsNetworkVLAN),

		// gendoc:generate(entity=network_routed, group=common, key=gvrp)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Register VLAN using GARP VLAN Registration Protocol
		"gvrp": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=network_routed, group=common, key=vrf)
		//
		// ---
		//  type: string
		//  shortdesc: VRF inherited by routed NICs linked to this network
		"vrf": validate.Optional(validate.IsAny),

		// gendoc:generate(entity=network_routed, group=ipv4, key=ipv4.subnets)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-separated list of IPv4 subnets that routed NIC addresses must be in and are assigned from
		"ipv4.subnets": validate.Optional(validate.IsListOf(validate.IsNetworkV4)),

		// gendoc:generate(entity=network_routed, group=ipv6, key=ipv6.subnets)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-separated list of IPv6 subnets that routed NIC addresses must be in and are assigned from
		"ipv6.subnets": validate.Optional(validate.IsListOf(validate.IsNetworkV6)),

		// gendoc:generate(entity=network_routed, group=ipv4, key=ipv4.dhcp)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to hand out the IPv4 address of virtual machine NICs over DHCP
		"ipv4.dhcp": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=network_routed, group=ipv6, key=ipv6.dhcp)
		//
		// ---
		//  type: bool
		//  defaultdesc: `true`
		//  shortdesc: Whether to hand out the IPv6 address of virtual machine NICs over DHCPv6
		"ipv6.dhcp": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=network_routed, group=common, key=dns.nameservers)
		//
		// ---
		//  type: string
		//  shortdesc: DNS server IPs to advertise to DHCP clients
		"dns.nameservers": validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),

		// gendoc:generate(entity=network_routed, group=ipv4, key=ipv4.gateway)
		//
		// ---
		//  type: string
		//  defaultdesc: `auto`
		//  shortdesc: IPv4 gateway mode inherited by routed NICs (`auto` or `none`)
		"ipv4.gateway": validate.Optional(validate.IsOneOf("auto", "none")),

		// gendoc:generate(entity=network_routed, group=ipv6, key=ipv6.gateway)
		//
		// ---
		//  type: string
		//  defaultdesc: `auto`
		//  shortdesc: IPv6 gateway mode inherited by routed NICs (`auto` or `none`)
		"ipv6.gateway": validate.Optional(validate.IsOneOf("auto", "none")),

		// gendoc:generate(entity=network_routed, group=ipv4, key=ipv4.host_address)
		//
		// ---
		//  type: string
		//  shortdesc: Host-side IPv4 gateway address inherited by routed NICs
		"ipv4.host_address": validate.Optional(validate.IsNetworkAddressV4),

		// gendoc:generate(entity=network_routed, group=ipv6, key=ipv6.host_address)
		//
		// ---
		//  type: string
		//  shortdesc: Host-side IPv6 gateway address inherited by routed NICs
		"ipv6.host_address": validate.Optional(validate.IsNetworkAddressV6),

		// gendoc:generate(entity=network_routed, group=ipv4, key=ipv4.host_tables)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-separated routing table IDs inherited by routed NICs
		"ipv4.host_tables": validate.Optional(validate.IsListOf(validate.IsInRange(0, 255))),

		// gendoc:generate(entity=network_routed, group=ipv6, key=ipv6.host_tables)
		//
		// ---
		//  type: string
		//  shortdesc: Comma-separated routing table IDs inherited by routed NICs
		"ipv6.host_tables": validate.Optional(validate.IsListOf(validate.IsInRange(0, 255))),
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	if config["vlan"] != "" && config["parent"] == "" {
		return fmt.Errorf("The %q setting can only be used when %q is set", "vlan", "parent")
	}

	return nil
}

// Info returns the driver information.
func (n *routed) Info() Info {
	info := n.common.Info()
	info.NodeSpecificConfig = true
	info.Projects = false

	return info
}

// Delete deletes a network.
func (n *routed) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	err := apparmor.NetworkDelete(n.state.OS, n)
	if err != nil {
		return err
	}

	return n.delete(clientType)
}

// Rename renames a network.
func (n *routed) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// The AppArmor profile of the DHCP servers is named after the network.
	err := apparmor.NetworkDelete(n.state.OS, n)
	if err != nil {
		return err
	}

	err = n.rename(newName)
	if err != nil {
		return err
	}

	return apparmor.NetworkLoad(n.state.OS, n)
}

// Start starts the network.
func (n *routed) Start() error {
	n.logger.Debug("Start")

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { n.setUnavailable() })

	err := n.setup(n.config["parent"])
	if err != nil {
		return err
	}

	// The per-NIC DHCP servers run under the network's dnsmasq profile.
	err = apparmor.NetworkLoad(n.state.OS, n)
	if err != nil {
		return err
	}

	reverter.Success()
	n.setAvailable()

	return nil
}

func (n *routed) setup(parent string) error {
	if parent == "" {
		return nil
	}

	if !InterfaceExists(parent) {
		return fmt.Errorf("Parent interface %q not found", parent)
	}

	link := &ip.Link{Name: parent}
	err := link.SetUp()
	if err != nil {
		return fmt.Errorf("Failed to bring up the host interface %q: %w", parent, err)
	}

	return nil
}

// Stop stops the network.
func (n *routed) Stop() error {
	n.logger.Debug("Stop")
	return nil
}

// Update updates the network.
func (n *routed) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeded, changedKeys, oldNetwork, err := n.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeded {
		return nil
	}

	if n.Status() == api.NetworkStatusPending || n.LocalStatus() == api.NetworkStatusPending {
		return n.update(newNetwork, targetNode, clientType)
	}

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() {
		_ = n.update(oldNetwork, targetNode, clientType)
	})

	if slices.Contains(changedKeys, "parent") {
		err = n.setup(newNetwork.Config["parent"])
		if err != nil {
			return err
		}
	}

	err = n.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	reverter.Success()

	return nil
}

// FillConfig sets routed network defaults.
func (n *routed) FillConfig(config map[string]string) error {
	if config["ipv4.gateway"] == "" {
		config["ipv4.gateway"] = "auto"
	}

	if config["ipv6.gateway"] == "" {
		config["ipv6.gateway"] = "auto"
	}

	return nil
}

// ValidateName validates network name.
func (n *routed) ValidateName(name string) error {
	return n.common.ValidateName(name)
}

// Create creates a network.
func (n *routed) Create(clientType request.ClientType) error {
	return n.common.Create(clientType)
}
