package ip

import (
	"fmt"

	"github.com/vishvananda/netlink"

	"github.com/lxc/incus/v6/shared/units"
)

// Class represents qdisc class object.
type Class struct {
	Dev     string
	Parent  string
	Classid string
}

// ClassHTB represents htb qdisc class object.
type ClassHTB struct {
	Class
	Rate string
}

// Add adds class to a node.
func (class *ClassHTB) Add() error {
	link, err := linkByName(class.Dev)
	if err != nil {
		return err
	}

	parent, err := parseHandle(class.Parent)
	if err != nil {
		return err
	}

	classAttrs := netlink.ClassAttrs{
		LinkIndex:  link.Attrs().Index,
		Parent:     parent,
		Statistics: nil,
	}

	htbClassAttrs := netlink.HtbClassAttrs{}

	if class.Classid != "" {
		handle, err := parseHandle(class.Classid)
		if err != nil {
			return err
		}

		classAttrs.Handle = handle
	}

	if class.Rate != "" {
		rate, err := units.ParseBitSizeString(class.Rate)
		if err != nil {
			return fmt.Errorf("Invalid rate %q: %w", class.Rate, err)
		}

		htbClassAttrs.Rate = uint64(rate)
	}

	err = netlink.ClassAdd(netlink.NewHtbClass(classAttrs, htbClassAttrs))
	if err != nil {
		return fmt.Errorf("Failed to add htb class: %w", err)
	}

	return nil
}
