package network

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"

	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// wireguard represents a wireguard network.
type wireguard struct {
	common
}

// DBType returns the network type DB ID.
func (n *wireguard) DBType() db.NetworkType {
	return db.NetworkTypeWireguard
}

// Config returns the network configuration, filtering out listen_port if it's empty or "0".
func (n *wireguard) Config() map[string]string {
	config := n.common.Config()

	// Hide listen_port if it's empty or "0"
	if config["listen_port"] == "" || config["listen_port"] == "0" {
		// Create a new map without listen_port
		filteredConfig := make(map[string]string, len(config))
		for k, v := range config {
			if k != "listen_port" {
				filteredConfig[k] = v
			}
		}
		return filteredConfig
	}

	return config
}

// Validate network config.
func (n *wireguard) Validate(config map[string]string, clientType request.ClientType) error {
	rules := map[string]func(value string) error{
		// gendoc:generate(entity=network_wireguard, group=common, key=interface)
		//
		// ---
		// type: string
		// condition: -
		// defaultdesc: Network name
		// shortdesc: WireGuard interface name
		"interface": validate.Optional(validate.IsInterfaceName),

		// gendoc:generate(entity=network_wireguard, group=common, key=private_key)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: WireGuard private key (base64 encoded). If not provided, one will be generated.
		"private_key": validate.Optional(validate.IsNotEmpty),

		// gendoc:generate(entity=network_wireguard, group=common, key=listen_port)
		//
		// ---
		// type: integer
		// condition: -
		// defaultdesc: Auto-assigned by WireGuard
		// shortdesc: UDP port to listen on. If not specified or set to 0, WireGuard will automatically assign an available port.
		"listen_port": validate.Optional(validate.IsInt64),

		// gendoc:generate(entity=network_wireguard, group=common, key=mtu)
		//
		// ---
		// type: integer
		// condition: -
		// defaultdesc: `1420`
		// shortdesc: The MTU of the WireGuard interface
		"mtu": validate.Optional(validate.IsNetworkMTU),

		// gendoc:generate(entity=network_wireguard, group=common, key=ipv4.address)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: Comma-separated list of IPv4 addresses and CIDR for the WireGuard interface (e.g., 10.0.0.1/24)
		"ipv4.address": validate.Optional(validate.IsListOf(validate.IsNetworkAddressCIDR)),

		// gendoc:generate(entity=network_wireguard, group=common, key=ipv6.address)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: Comma-separated list of IPv6 addresses and CIDR for the WireGuard interface (e.g., 2001:db8::1/64)
		"ipv6.address": validate.Optional(validate.IsListOf(validate.IsNetworkAddressCIDR)),

		// gendoc:generate(entity=network_wireguard, group=peers, key=peers.NAME.public_key)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: Public key of peer NAME

		// gendoc:generate(entity=network_wireguard,group=peers, key=peers.NAME.allowed_ips)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: Allowed IPs for peer NAME (comma-separated CIDR addresses)

		// gendoc:generate(entity=network_wireguard, group=peers, key=peers.NAME.endpoint)
		//
		// ---
		// type: string
		// condition: -
		// shortdesc: Endpoint address for peer NAME (IP:port or hostname:port)

		// gendoc:generate(entity=network_wireguard, group=peers, key=peers.NAME.persistent_keepalive)
		//
		// ---
		// type: integer
		// condition: -
		// shortdesc: Persistent keep-alive interval in seconds for peer NAME

		// gendoc:generate(entity=network_wireguard, group=common, key=user.*)
		//
		// ---
		// type: string
		// shortdesc: User-provided free-form key/value pairs
	}

	// Add validation rules for peer configurations
	for key := range config {
		if !strings.HasPrefix(key, "peers.") {
			continue
		}

		parts := strings.SplitN(key, ".", 3)
		if len(parts) != 3 {
			continue
		}

		peerKey := parts[2]

		// Add the correct validation rule for the dynamic field based on last part of key.
		switch peerKey {
		case "public_key":
			rules[key] = validate.Optional(validate.IsNotEmpty)
		case "allowed_ips":
			rules[key] = validate.Optional(validate.IsAny)
		case "endpoint":
			rules[key] = validate.Optional(validate.IsAny)
		case "persistent_keepalive":
			rules[key] = validate.Optional(validate.IsInt64)
		}
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	// Validate peer configurations
	for key, value := range config {
		if strings.HasPrefix(key, "peers.") {
			parts := strings.SplitN(key, ".", 3)
			if len(parts) == 3 {
				peerName := parts[1]
				peerKey := parts[2]

				switch peerKey {
				case "public_key":
					if value == "" {
						return fmt.Errorf("Peer %q public_key cannot be empty", peerName)
					}
					// Basic validation for base64 key (44 chars for WireGuard keys)
					if len(value) != 44 {
						return fmt.Errorf("Peer %q public_key must be 44 characters (base64 encoded)", peerName)
					}
				case "allowed_ips":
					if value == "" {
						return fmt.Errorf("Peer %q allowed_ips cannot be empty", peerName)
					}
					// Validate each CIDR in the comma-separated list
					ips := util.SplitNTrimSpace(value, ",", -1, true)
					for _, ipStr := range ips {
						if err := validate.IsNetworkAddressCIDR(ipStr); err != nil {
							return fmt.Errorf("Peer %q allowed_ips contains invalid CIDR %q: %w", peerName, ipStr, err)
						}
					}
				case "endpoint":
					// Endpoint is optional, but if provided should be valid
					if value != "" {
						parts := strings.Split(value, ":")
						if len(parts) != 2 {
							return fmt.Errorf("Peer %q endpoint must be in format IP:port or hostname:port", peerName)
						}
						port, err := strconv.ParseUint(parts[1], 10, 16)
						if err != nil || port == 0 || port > 65535 {
							return fmt.Errorf("Peer %q endpoint port must be a valid port number (1-65535)", peerName)
						}
					}
				case "persistent_keepalive":
					if value != "" {
						if err := validate.IsInt64(value); err != nil {
							return fmt.Errorf("Peer %q persistent_keepalive must be an integer: %w", peerName, err)
						}
					}
				}
			}
		}
	}

	return nil
}

// Create creates the WireGuard network.
func (n *wireguard) Create(clientType request.ClientType) error {
	n.logger.Debug("Create", logger.Ctx{"clientType": clientType, "config": n.config})

	// Check if wg command is available
	_, err := exec.LookPath("wg")
	if err != nil {
		return fmt.Errorf("WireGuard tools not found. Please install wireguard-tools package")
	}

	return nil
}

// Delete deletes a network.
func (n *wireguard) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	err := n.Stop()
	if err != nil {
		return err
	}

	return n.delete(clientType)
}

// Rename renames a network.
func (n *wireguard) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts the network.
func (n *wireguard) Start() error {
	n.logger.Debug("Start")

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { n.setUnavailable() })

	err := n.setup()
	if err != nil {
		return err
	}

	reverter.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

// setup creates and configures the WireGuard interface.
func (n *wireguard) setup() error {
	n.logger.Debug("Setting up WireGuard network")

	// Determine interface name
	ifaceName := n.config["interface"]
	if ifaceName == "" {
		ifaceName = n.name
	}

	// Check if interface already exists
	if !InterfaceExists(ifaceName) {
		// Create WireGuard interface using ip command
		cmd := exec.Command("ip", "link", "add", ifaceName, "type", "wireguard")
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("Failed to create WireGuard interface %q: %w", ifaceName, err)
		}
	}

	// Set MTU if specified
	if n.config["mtu"] != "" {
		mtu, err := strconv.ParseUint(n.config["mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Invalid MTU %q: %w", n.config["mtu"], err)
		}

		link := &ip.Link{Name: ifaceName}
		err = link.SetMTU(uint32(mtu))
		if err != nil {
			return fmt.Errorf("Failed setting MTU %q on %q: %w", n.config["mtu"], ifaceName, err)
		}
	} else {
		// Set default MTU for WireGuard (1420 is recommended)
		link := &ip.Link{Name: ifaceName}
		err := link.SetMTU(1420)
		if err != nil {
			n.logger.Warn("Failed to set default MTU 1420", logger.Ctx{"error": err})
		}
	}

	// Configure WireGuard using wg command
	err := n.configureWireGuard(ifaceName)
	if err != nil {
		return err
	}

	// Bring interface up first (needed for WireGuard to assign port when using 0)
	link := &ip.Link{Name: ifaceName}
	err = link.SetUp()
	if err != nil {
		return fmt.Errorf("Failed to bring up WireGuard interface %q: %w", ifaceName, err)
	}

	// Set IP addresses if specified (supports multiple addresses, IPv4 and IPv6)
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		addrKey := fmt.Sprintf("%s.address", keyPrefix)
		if n.config[addrKey] != "" {
			addresses := util.SplitNTrimSpace(n.config[addrKey], ",", -1, true)
			for _, addrStr := range addresses {
				ipAddress, ipNet, err := net.ParseCIDR(addrStr)
				if err != nil {
					return fmt.Errorf("Failed to parse address %q: %w", addrStr, err)
				}
				// Create IPNet with the specific IP address (not the network address)
				addr := &ip.Addr{
					DevName: ifaceName,
					Address: &net.IPNet{
						IP:   ipAddress,
						Mask: ipNet.Mask,
					},
				}
				// Check if address already exists before adding
				addressExists, err := n.addressExists(ifaceName, ipAddress)
				if err != nil {
					return fmt.Errorf("Failed to check if address exists on %q: %w", ifaceName, err)
				}
				if !addressExists {
					err = addr.Add()
					if err != nil {
						// Check if error is "file exists" (address already assigned)
						if !strings.Contains(err.Error(), "file exists") && !strings.Contains(err.Error(), "already assigned") {
							return fmt.Errorf("Failed to set address %q on %q: %w", addrStr, ifaceName, err)
						}
						n.logger.Debug("Address already exists on interface, skipping", logger.Ctx{"address": addrStr, "interface": ifaceName})
					}
				}
			}
		}
	}

	return nil
}

// configureWireGuard configures the WireGuard interface using the wg command.
func (n *wireguard) configureWireGuard(ifaceName string) error {
	// Generate private key if not provided
	privateKey := n.config["private_key"]
	if privateKey == "" {
		// Generate private key using wg genkey
		cmd := exec.Command("wg", "genkey")
		output, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("Failed to generate WireGuard private key: %w", err)
		}
		privateKey = strings.TrimSpace(string(output))

		// Store the generated key in config
		n.config["private_key"] = privateKey
		err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateNetwork(ctx, n.project, n.name, n.description, n.config)
		})
		if err != nil {
			return fmt.Errorf("Failed to save generated private key: %w", err)
		}
	}

	// Set private key
	cmd := exec.Command("wg", "set", ifaceName, "private-key", "/dev/stdin")
	cmd.Stdin = strings.NewReader(privateKey)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("Failed to set WireGuard private key: %w", err)
	}

	// Set listen port only if a valid port is specified (not empty and not "0")
	listenPort := n.config["listen_port"]
	if listenPort != "" && listenPort != "0" {
		cmd = exec.Command("wg", "set", ifaceName, "listen-port", listenPort)
		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("Failed to set WireGuard listen port: %w", err)
		}
	}

	// Add peers
	peers := make(map[string]map[string]string)
	for key, value := range n.config {
		if strings.HasPrefix(key, "peers.") {
			parts := strings.SplitN(key, ".", 3)
			if len(parts) == 3 {
				peerName := parts[1]
				peerKey := parts[2]

				if peers[peerName] == nil {
					peers[peerName] = make(map[string]string)
				}
				peers[peerName][peerKey] = value
			}
		}
	}

	// Configure each peer
	for peerName, peerConfig := range peers {
		publicKey, ok := peerConfig["public_key"]
		if !ok || publicKey == "" {
			n.logger.Warn("Skipping peer without public_key", logger.Ctx{"peer": peerName})
			continue
		}

		// Build wg set command for peer
		args := []string{"set", ifaceName, "peer", publicKey}

		if allowedIPs, ok := peerConfig["allowed_ips"]; ok && allowedIPs != "" {
			args = append(args, "allowed-ips", allowedIPs)
		}

		if endpoint, ok := peerConfig["endpoint"]; ok && endpoint != "" {
			args = append(args, "endpoint", endpoint)
		}

		if keepalive, ok := peerConfig["persistent_keepalive"]; ok && keepalive != "" {
			args = append(args, "persistent-keepalive", keepalive)
		}

		cmd := exec.Command("wg", args...)
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("Failed to configure peer %q: %w", peerName, err)
		}
	}

	return nil
}

// Stop stops the network.
func (n *wireguard) Stop() error {
	n.logger.Debug("Stop")

	// Determine interface name
	ifaceName := n.config["interface"]
	if ifaceName == "" {
		ifaceName = n.name
	}

	// Remove IP addresses if specified and interface exists (supports multiple addresses)
	for _, keyPrefix := range []string{"ipv4", "ipv6"} {
		addrKey := fmt.Sprintf("%s.address", keyPrefix)
		if n.config[addrKey] != "" && InterfaceExists(ifaceName) {
			addresses := util.SplitNTrimSpace(n.config[addrKey], ",", -1, true)
			for _, addrStr := range addresses {
				ipAddress, _, err := net.ParseCIDR(addrStr)
				if err == nil {
					err = n.removeAddress(ifaceName, ipAddress)
					if err != nil {
						n.logger.Warn("Failed to remove address from interface", logger.Ctx{"address": addrStr, "interface": ifaceName, "error": err})
					}
				}
			}
		}
	}

	// Remove WireGuard interface if it exists
	if InterfaceExists(ifaceName) {
		link := &ip.Link{Name: ifaceName}
		err := link.Delete()
		if err != nil {
			return fmt.Errorf("Failed to remove WireGuard interface %q: %w", ifaceName, err)
		}
	}

	return nil
}

// addressExists checks if the given IP address already exists on the interface.
func (n *wireguard) addressExists(ifaceName string, ipAddress net.IP) (bool, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return false, err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return false, err
	}

	for _, addr := range addrs {
		addrIP, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		if addrIP.Equal(ipAddress) {
			return true, nil
		}
	}

	return false, nil
}

// removeAddress removes the given IP address from the interface.
func (n *wireguard) removeAddress(ifaceName string, ipAddress net.IP) error {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return fmt.Errorf("Failed to find link %q: %w", ifaceName, err)
	}

	// Get all addresses on the interface
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("Failed to list addresses on %q: %w", ifaceName, err)
	}

	// Find and remove the matching address
	for _, addr := range addrs {
		if addr.IP.Equal(ipAddress) {
			err = netlink.AddrDel(link, &addr)
			if err != nil {
				// Ignore error if address doesn't exist
				if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no such") {
					return fmt.Errorf("Failed to remove address %q from %q: %w", ipAddress.String(), ifaceName, err)
				}
			}
			return nil
		}
	}

	return nil
}

// Update updates the network.
func (n *wireguard) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeded, changedKeys, oldNetwork, err := n.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeded {
		return nil // Nothing changed.
	}

	// If the network as a whole has not had any previous creation attempts, or the node itself is still
	// pending, then don't apply the new settings to the node, just to the database record (ready for the
	// actual global create request to be initiated).
	if n.Status() == api.NetworkStatusPending || n.LocalStatus() == api.NetworkStatusPending {
		return n.update(newNetwork, targetNode, clientType)
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Define a function which reverts everything.
	reverter.Add(func() {
		// Reset changes to all nodes and database.
		_ = n.update(oldNetwork, targetNode, clientType)
	})

	// Check if interface name changed
	interfaceChanged := slices.Contains(changedKeys, "interface")

	if interfaceChanged {
		// Stop old interface
		err = n.Stop()
		if err != nil {
			return err
		}
	}

	// Apply changes to all nodes and database.
	err = n.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	// Restart with new configuration
	err = n.setup()
	if err != nil {
		return err
	}

	reverter.Success()

	return nil
}
