package ip

import (
	"github.com/vishvananda/netlink"
)

// Macvtap represents arguments for link of type macvtap.
type Macvtap struct {
	Macvlan
}

// Add adds new virtual link.
func (macvtap *Macvtap) Add() error {
	attrs, err := macvtap.netlinkAttrs()
	if err != nil {
		return err
	}

	return macvtap.addLink(&netlink.Macvtap{
		Macvlan: netlink.Macvlan{
			LinkAttrs: attrs,
			Mode:      macvtap.netlinkMode(),
		},
	})
}
