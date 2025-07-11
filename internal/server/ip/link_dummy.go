package ip

import (
	"github.com/vishvananda/netlink"
)

// Dummy represents arguments for link device of type dummy.
type Dummy struct {
	Link
}

// Add adds new virtual link.
func (d *Dummy) Add() error {
	attrs, err := d.netlinkAttrs()
	if err != nil {
		return err
	}

	return d.addLink(&netlink.Dummy{
		LinkAttrs: attrs,
	})
}
