package ip

import (
	"fmt"
	"net"
	"strconv"

	"github.com/vishvananda/netlink"
)

// Vxlan represents arguments for link of type vxlan.
type Vxlan struct {
	Link
	VxlanID string
	DevName string
	Local   string
	Remote  string
	Group   string
	DstPort string
	TTL     string
}

// Add adds new virtual link.
func (vxlan *Vxlan) Add() error {
	attrs, err := vxlan.netlinkAttrs()
	if err != nil {
		return err
	}

	// TODO: all of these these can be passed as int or net.IP

	vxlanID, err := strconv.Atoi(vxlan.VxlanID)
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
	if vxlan.Group != "" {
		group = net.ParseIP(vxlan.Group)
		if group == nil {
			return fmt.Errorf("invalid group address %q", vxlan.Group)
		}

		if !group.IsMulticast() {
			return fmt.Errorf("group address must be multicast, got %q", vxlan.Group)
		}
	}

	if vxlan.Remote != "" {
		if group != nil {
			return fmt.Errorf("group and remote can not be specified together")
		}

		group = net.ParseIP(vxlan.Remote)
		if group == nil {
			return fmt.Errorf("invalid remote address %q", vxlan.Remote)
		}

		if group.IsMulticast() {
			return fmt.Errorf("remote address must not be multicast, got %q", vxlan.Remote)
		}
	}

	var local net.IP
	if vxlan.Local != "" {
		local = net.ParseIP(vxlan.Local)
		if local == nil {
			return fmt.Errorf("invalid local address %q", vxlan.Local)
		}
	}

	var ttl int
	if vxlan.TTL != "" {
		ttl, err = strconv.Atoi(vxlan.TTL)
		if err != nil {
			return err
		}
	}

	var dstport int
	if vxlan.DstPort != "" {
		dstport, err = strconv.Atoi(vxlan.DstPort)
		if err != nil {
			return err
		}
	}

	return netlink.LinkAdd(&netlink.Vxlan{
		LinkAttrs:    attrs,
		VxlanId:      vxlanID,
		VtepDevIndex: devIndex,
		SrcAddr:      local,
		Group:        group,
		TTL:          ttl,
		Port:         dstport,
	})
}
