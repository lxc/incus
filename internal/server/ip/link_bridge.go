package ip

import (
	"github.com/vishvananda/netlink"
)

// Bridge represents arguments for link device of type bridge.
type Bridge struct {
	Link
}

// Add adds new virtual link.
func (b *Bridge) Add() error {
	attrs, err := b.netlinkAttrs()
	if err != nil {
		return err
	}

	return b.addLink(&netlink.Bridge{
		LinkAttrs: attrs,
	})
}
