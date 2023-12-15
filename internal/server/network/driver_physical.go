package network

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// physical represents a physical network.
type physical struct {
	common
}

// DBType returns the network type DB ID.
func (n *physical) DBType() db.NetworkType {
	return db.NetworkTypePhysical
}

// Validate network config.
func (n *physical) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent":                      validate.Required(validate.IsNotEmpty, validate.IsInterfaceName),
		"mtu":                         validate.Optional(validate.IsNetworkMTU),
		"vlan":                        validate.Optional(validate.IsNetworkVLAN),
		"gvrp":                        validate.Optional(validate.IsBool),
		"ipv4.gateway":                validate.Optional(validate.IsNetworkAddressCIDRV4),
		"ipv6.gateway":                validate.Optional(validate.IsNetworkAddressCIDRV6),
		"ipv4.ovn.ranges":             validate.Optional(validate.IsListOf(validate.IsNetworkRangeV4)),
		"ipv6.ovn.ranges":             validate.Optional(validate.IsListOf(validate.IsNetworkRangeV6)),
		"ipv4.routes":                 validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		"ipv4.routes.anycast":         validate.Optional(validate.IsBool),
		"ipv6.routes":                 validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		"ipv6.routes.anycast":         validate.Optional(validate.IsBool),
		"dns.nameservers":             validate.Optional(validate.IsListOf(validate.IsNetworkAddress)),
		"ovn.ingress_mode":            validate.Optional(validate.IsOneOf("l2proxy", "routed")),
		"volatile.last_state.created": validate.Optional(validate.IsBool),
	}

	// Add the BGP validation rules.
	bgpRules, err := n.bgpValidationRules(config)
	if err != nil {
		return err
	}

	for k, v := range bgpRules {
		rules[k] = v
	}

	// Validate the configuration.
	err = n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// checkParentUse checks if parent is already in use by another network or instance device.
func (n *physical) checkParentUse(ourConfig map[string]string) (bool, error) {
	// Get all managed networks across all projects.
	var err error
	var projectNetworks map[string]map[int64]api.Network

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("Failed to load all networks: %w", err)
	}

	for projectName, networks := range projectNetworks {
		if projectName != api.ProjectDefaultName {
			continue // Only default project networks can possibly reference a physical interface.
		}

		for _, network := range networks {
			if network.Name == n.name {
				continue // Ignore our own DB record.
			}

			// Check if another network is using our parent.
			if network.Config["parent"] == ourConfig["parent"] {
				// If either network doesn't specify a vlan, or both specify same vlan,
				// then we can't use this parent.
				if (network.Config["vlan"] == "" || ourConfig["vlan"] == "") || network.Config["vlan"] == ourConfig["vlan"] {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// Create checks whether the referenced parent interface is used by other networks or instance devices, as we
// need to have exclusive access to the interface.
func (n *physical) Create(clientType request.ClientType) error {
	n.logger.Debug("Create", logger.Ctx{"clientType": clientType, "config": n.config})

	// We only need to check in the database once, not on every clustered node.
	if clientType == request.ClientTypeNormal {
		inUse, err := n.checkParentUse(n.config)
		if err != nil {
			return err
		}

		if inUse {
			return fmt.Errorf("Parent interface %q in use by another network", n.config["parent"])
		}
	}

	return nil
}

// Delete deletes a network.
func (n *physical) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	err := n.Stop()
	if err != nil {
		return err
	}

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *physical) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start sets up some global configuration.
func (n *physical) Start() error {
	n.logger.Debug("Start")

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { n.setUnavailable() })

	err := n.setup(nil)
	if err != nil {
		return err
	}

	revert.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

func (n *physical) setup(oldConfig map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	if !InterfaceExists(n.config["parent"]) {
		return fmt.Errorf("Parent interface %q not found", n.config["parent"])
	}

	hostName := GetHostDevice(n.config["parent"], n.config["vlan"])

	created, err := VLANInterfaceCreate(n.config["parent"], hostName, n.config["vlan"], util.IsTrue(n.config["gvrp"]))
	if err != nil {
		return err
	}

	if created {
		revert.Add(func() { _ = InterfaceRemove(hostName) })
	}

	// Set the MTU.
	if n.config["mtu"] != "" {
		mtu, err := strconv.ParseUint(n.config["mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Invalid MTU %q: %w", n.config["mtu"], err)
		}

		phyLink := &ip.Link{Name: hostName}
		err = phyLink.SetMTU(uint32(mtu))
		if err != nil {
			return fmt.Errorf("Failed setting MTU %q on %q: %w", n.config["mtu"], phyLink.Name, err)
		}
	}

	// Record if we created this device or not (if we have not already recorded that we created it previously),
	// so it can be removed on stop. This way we won't overwrite the setting on daemon restart.
	if util.IsFalseOrEmpty(n.config["volatile.last_state.created"]) {
		n.config["volatile.last_state.created"] = fmt.Sprintf("%t", created)
		err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateNetwork(ctx, n.project, n.name, n.description, n.config)
		})
		if err != nil {
			return fmt.Errorf("Failed saving volatile config: %w", err)
		}
	}

	// Setup BGP.
	err = n.bgpSetup(oldConfig)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Stop stops is a no-op.
func (n *physical) Stop() error {
	n.logger.Debug("Stop")

	// Clear BGP.
	err := n.bgpClear(n.config)
	if err != nil {
		return err
	}

	hostName := GetHostDevice(n.config["parent"], n.config["vlan"])

	// Only try and remove created VLAN interfaces.
	if n.config["vlan"] != "" && util.IsTrue(n.config["volatile.last_state.created"]) && InterfaceExists(hostName) {
		err := InterfaceRemove(hostName)
		if err != nil {
			return err
		}
	}

	// Reset MTU back to 1500 if overridden in config.
	if n.config["mtu"] != "" && InterfaceExists(hostName) {
		var resetMTU uint32 = 1500
		link := &ip.Link{Name: hostName}
		err := link.SetMTU(1500)
		if err != nil {
			return fmt.Errorf("Failed setting MTU %d on %q: %w", resetMTU, link.Name, err)
		}
	}

	// Remove last state config.
	delete(n.config, "volatile.last_state.created")
	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNetwork(ctx, n.project, n.name, n.description, n.config)
	})
	if err != nil {
		return fmt.Errorf("Failed removing volatile config: %w", err)
	}

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *physical) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeded, changedKeys, oldNetwork, err := n.common.configChanged(newNetwork)
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
		return n.common.update(newNetwork, targetNode, clientType)
	}

	revert := revert.New()
	defer revert.Fail()

	hostNameChanged := util.ValueInSlice("vlan", changedKeys) || util.ValueInSlice("parent", changedKeys)

	// We only need to check in the database once, not on every clustered node.
	if clientType == request.ClientTypeNormal {
		if hostNameChanged {
			isUsed, err := n.IsUsed()
			if isUsed || err != nil {
				return fmt.Errorf("Cannot update network parent interface when in use")
			}

			inUse, err := n.checkParentUse(newNetwork.Config)
			if err != nil {
				return err
			}

			if inUse {
				return fmt.Errorf("Parent interface %q in use by another network", newNetwork.Config["parent"])
			}
		}
	}

	if hostNameChanged {
		err = n.Stop()
		if err != nil {
			return err
		}

		// Remove the volatile last state from submitted new config if present.
		delete(newNetwork.Config, "volatile.last_state.created")
	}

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		_ = n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and databse.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	err = n.setup(oldNetwork.Config)
	if err != nil {
		return err
	}

	revert.Success()

	// Notify dependent networks (those using this network as their uplink) of the changes.
	// Do this after the network has been successfully updated so that a failure to notify a dependent network
	// doesn't prevent the network itself from being updated.
	if clientType == request.ClientTypeNormal && len(changedKeys) > 0 {
		n.common.notifyDependentNetworks(changedKeys)
	}

	return nil
}

// DHCPv4Subnet returns the DHCPv4 subnet (if DHCP is enabled on network).
func (n *physical) DHCPv4Subnet() *net.IPNet {
	_, subnet, err := net.ParseCIDR(n.config["ipv4.gateway"])
	if err != nil {
		return nil
	}

	return subnet
}

// DHCPv6Subnet returns the DHCPv6 subnet (if DHCP or SLAAC is enabled on network).
func (n *physical) DHCPv6Subnet() *net.IPNet {
	_, subnet, err := net.ParseCIDR(n.config["ipv6.gateway"])
	if err != nil {
		return nil
	}

	return subnet
}
