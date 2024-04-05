package ovn

import (
	"context"

	ovsdbClient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/ovsdb"

	ovnICNB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-ic-nb"
)

// CreateTransitSwitch creates a new managed transit switch.
func (o *ICNB) CreateTransitSwitch(ctx context.Context, name string, mayExist bool) error {
	// Look for an existing transit switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: name,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil && err != ovsdbClient.ErrNotFound {
		return err
	}

	// Handle existing switches.
	if transitSwitch.UUID != "" {
		if !mayExist {
			return ErrExists
		}

		return nil
	}

	// Mark new switches as managed by Incus.
	transitSwitch.ExternalIDs = map[string]string{"incus-managed": "true"}

	// Create the switch.
	ops, err := o.client.Create(&transitSwitch)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, ops...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, ops)
	if err != nil {
		return err
	}

	return nil
}

// DeleteTransitSwitch deletes an existing transit switch.
// The force parameter is required to delete a transit switch which wasn't created by Incus.
func (o *ICNB) DeleteTransitSwitch(ctx context.Context, name string, force bool) error {
	// Get the current transit switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: name,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil {
		// Already deleted.
		if err == ErrNotFound {
			return nil
		}

		return err
	}

	// Check if the switch is incus-managed.
	if !force && transitSwitch.ExternalIDs["incus-managed"] != "true" {
		return ErrNotManaged
	}

	// Delete the switch.
	deleteOps, err := o.client.Where(&transitSwitch).Delete()
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, deleteOps...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, deleteOps)
	if err != nil {
		return err
	}

	return nil
}
