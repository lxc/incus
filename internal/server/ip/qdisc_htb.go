package ip

import (
	"fmt"
	"strconv"

	"github.com/vishvananda/netlink"
)

// QdiscHTB represents the hierarchy token bucket qdisc object.
type QdiscHTB struct {
	Qdisc
	Default string
}

// Add adds a htb qdisc to a device.
func (q *QdiscHTB) Add() error {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return err
	}

	htb := netlink.NewHtb(attrs)

	if q.Default != "" {
		defcls, err := strconv.Atoi(q.Default)
		if err != nil {
			return fmt.Errorf("invalid htb default class %q: %w", q.Default, err)
		}

		htb.Defcls = uint32(defcls)
	}

	err = netlink.QdiscAdd(htb)
	if err != nil {
		return fmt.Errorf("failed to add qdisc htb %v: %w", htb, mapQdiscErr(err))
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
		return fmt.Errorf("failed to delete qdisc htb %v: %w", htb, mapQdiscErr(err))
	}

	return nil
}
