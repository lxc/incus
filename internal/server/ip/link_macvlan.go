package ip

import (
	"github.com/vishvananda/netlink"
)

// Macvlan represents arguments for link of type macvlan.
type Macvlan struct {
	Link
	Mode string
}

func (macvlan *Macvlan) netlinkMode() netlink.MacvlanMode {
	switch macvlan.Mode {
	case "default":
		return netlink.MACVLAN_MODE_DEFAULT
	case "private":
		return netlink.MACVLAN_MODE_PRIVATE
	case "vepa":
		return netlink.MACVLAN_MODE_VEPA
	case "bridge":
		return netlink.MACVLAN_MODE_BRIDGE
	case "passthru":
		return netlink.MACVLAN_MODE_PASSTHRU
	case "source":
		return netlink.MACVLAN_MODE_SOURCE
	default:
		return netlink.MACVLAN_MODE_DEFAULT
	}
}

// Add adds new virtual link.
func (macvlan *Macvlan) Add() error {
	attrs, err := macvlan.netlinkAttrs()
	if err != nil {
		return err
	}

	return macvlan.addLink(&netlink.Macvlan{
		LinkAttrs: attrs,
		Mode:      macvlan.netlinkMode(),
	})
}
