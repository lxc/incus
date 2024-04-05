package network

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/validate"
)

// sriov represents an sriov network.
type sriov struct {
	common
}

// DBType returns the network type DB ID.
func (n *sriov) DBType() db.NetworkType {
	return db.NetworkTypeSriov
}

// Validate network config.
func (n *sriov) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent": validate.Required(validate.IsNotEmpty, validate.IsInterfaceName),
		"mtu":    validate.Optional(validate.IsNetworkMTU),
		"vlan":   validate.Optional(validate.IsNetworkVLAN),
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes a network.
func (n *sriov) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *sriov) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *sriov) Start() error {
	n.logger.Debug("Start")

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { n.setUnavailable() })

	if !InterfaceExists(n.config["parent"]) {
		return fmt.Errorf("Parent interface %q not found", n.config["parent"])
	}

	revert.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

// Stop stops is a no-op.
func (n *sriov) Stop() error {
	n.logger.Debug("Stop")

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *sriov) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
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

	revert := revert.New()
	defer revert.Fail()

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

	revert.Success()
	return nil
}
