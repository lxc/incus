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
	Local   string
	Remote  string
	Group   string
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

	// TODO: all of these these can be passed net.IP
	var group net.IP
	if vxlan.Group != "" {
		group = net.ParseIP(vxlan.Group)
		if group == nil {
			return fmt.Errorf("Invalid group address %q", vxlan.Group)
		}

		if !group.IsMulticast() {
			return fmt.Errorf("Group address must be multicast, got %q", vxlan.Group)
		}
	}

	if vxlan.Remote != "" {
		if group != nil {
			return fmt.Errorf("Group and remote can not be specified together")
		}

		group = net.ParseIP(vxlan.Remote)
		if group == nil {
			return fmt.Errorf("Invalid remote address %q", vxlan.Remote)
		}

		if group.IsMulticast() {
			return fmt.Errorf("Remote address must not be multicast, got %q", vxlan.Remote)
		}
	}

	var local net.IP
	if vxlan.Local != "" {
		local = net.ParseIP(vxlan.Local)
		if local == nil {
			return fmt.Errorf("Invalid local address %q", vxlan.Local)
		}
	}

	return netlink.LinkAdd(&netlink.Vxlan{
		LinkAttrs:    attrs,
		VxlanId:      vxlan.VxlanID,
		VtepDevIndex: devIndex,
		SrcAddr:      local,
		Group:        group,
		TTL:          vxlan.TTL,
		Port:         vxlan.DstPort,
	})
}
