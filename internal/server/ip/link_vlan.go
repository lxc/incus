package ip

import (
	"fmt"
	"strconv"

	"github.com/vishvananda/netlink"
)

// Vlan represents arguments for link of type vlan.
type Vlan struct {
	Link
	VlanID string
	Gvrp   bool
}

// Add adds new virtual link.
func (vlan *Vlan) Add() error {
	attrs, err := vlan.netlinkAttrs()
	if err != nil {
		return err
	}

	id, err := strconv.Atoi(vlan.VlanID)
	if err != nil {
		return fmt.Errorf("Invalid VLAN ID: %w", err)
	}

	return vlan.addLink(&netlink.Vlan{
		LinkAttrs: attrs,
		VlanId:    id,
		Gvrp:      &vlan.Gvrp,
	})
}
