package network

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/validate"
)

// macvlan represents a macvlan network.
type macvlan struct {
	common
}

// DBType returns the network type DB ID.
func (n *macvlan) DBType() db.NetworkType {
	return db.NetworkTypeMacvlan
}

// Validate network config.
func (n *macvlan) Validate(config map[string]string, clientType request.ClientType) error {
	rules := map[string]func(value string) error{
		// gendoc:generate(entity=network_macvlan, group=common, key=parent)
		//
		// ---
		//  type: string
		//  condition: -
		//  shortdesc: Parent interface to create macvlan NICs on
		"parent": validate.Required(validate.IsNotEmpty, validate.IsInterfaceName),

		// gendoc:generate(entity=network_macvlan, group=common, key=mtu)
		//
		// ---
		//  type: int
		//  condition: -
		//  shortdesc: The MTU of the new interface
		"mtu": validate.Optional(validate.IsNetworkMTU),

		// gendoc:generate(entity=network_macvlan, group=common, key=vlan)
		//
		// ---
		//  type: int
		//  condition: -
		//  shortdesc: The VLAN ID to attach to
		"vlan": validate.Optional(validate.IsNetworkVLAN),

		// gendoc:generate(entity=network_macvlan, group=common, key=gvrp)
		//
		// ---
		//  type: bool
		//  condition: -
		//  default: `false`
		//  shortdesc: Register VLAN using GARP VLAN Registration Protocol
		"gvrp": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=network_macvlan, group=common, key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form key/value pairs
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes a network.
func (n *macvlan) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *macvlan) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *macvlan) Start() error {
	n.logger.Debug("Start")

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { n.setUnavailable() })

	if !InterfaceExists(n.config["parent"]) {
		return fmt.Errorf("Parent interface %q not found", n.config["parent"])
	}

	reverter.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

// Stop stops is a no-op.
func (n *macvlan) Stop() error {
	n.logger.Debug("Stop")

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *macvlan) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeded, _, oldNetwork, err := n.common.configChanged(newNetwork)
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

	reverter := revert.New()
	defer reverter.Fail()

	// Define a function which reverts everything.
	reverter.Add(func() {
		// Reset changes to all nodes and database.
		_ = n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and database.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	reverter.Success()

	return nil
}
