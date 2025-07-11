package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

// Vxlan represents arguments for link of type vxlan.
type Vxlan struct {
	Link
	VxlanID int
	DevName string
	Local   net.IP
	Remote  net.IP
	Group   net.IP
	DstPort int
	TTL     int
}

// Add adds new virtual link.
func (vxlan *Vxlan) Add() error {
	attrs, err := vxlan.netlinkAttrs()
	if err != nil {
		return err
	}

	var devIndex int
	if vxlan.DevName != "" {
		dev, err := linkByName(vxlan.DevName)
		if err != nil {
			return err
		}

		devIndex = dev.Attrs().Index
	}

	var group net.IP
	if vxlan.Group != nil {
		if !vxlan.Group.IsMulticast() {
			return fmt.Errorf("Group address must be multicast, got %q", vxlan.Group)
		}

		group = vxlan.Group
	}

	if vxlan.Remote != nil {
		if group != nil {
			return fmt.Errorf("Group and remote can not be specified together")
		}

		if vxlan.Remote.IsMulticast() {
			return fmt.Errorf("Remote address must not be multicast, got %q", vxlan.Remote)
		}

		group = vxlan.Remote
	}

	return vxlan.addLink(&netlink.Vxlan{
		LinkAttrs:    attrs,
		VxlanId:      vxlan.VxlanID,
		VtepDevIndex: devIndex,
		SrcAddr:      vxlan.Local,
		Group:        group,
		TTL:          vxlan.TTL,
		Port:         vxlan.DstPort,
	})
}
