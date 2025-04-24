package ip

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// QdiscHTB represents the hierarchy token bucket qdisc object.
type QdiscHTB struct {
	Qdisc
	Default uint32
}

// Add adds a htb qdisc to a device.
func (q *QdiscHTB) Add() error {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return err
	}

	htb := netlink.NewHtb(attrs)

	htb.Defcls = q.Default

	err = netlink.QdiscAdd(htb)
	if err != nil {
		return fmt.Errorf("Failed to add qdisc htb %v: %w", htb, mapQdiscErr(err))
	}

	return nil
}

// Delete deletes a htb qdisc from a device.
func (q *QdiscHTB) Delete() error {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return err
	}

	htb := netlink.NewHtb(attrs)

	err = netlink.QdiscDel(htb)
	if err != nil {
		return fmt.Errorf("Failed to delete qdisc htb %v: %w", htb, mapQdiscErr(err))
	}

	return nil
}
