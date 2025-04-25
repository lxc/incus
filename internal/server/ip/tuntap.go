package ip

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// Tuntap represents arguments for tuntap manipulation.
type Tuntap struct {
	Name       string
	Mode       string
	MultiQueue bool
	Master     string
}

// Add adds new tuntap interface.
func (t *Tuntap) Add() error {
	var mode netlink.TuntapMode

	switch t.Mode {
	case "tun":
		mode = netlink.TUNTAP_MODE_TUN
	case "tap":
		mode = netlink.TUNTAP_MODE_TAP
	default:
		return fmt.Errorf("Invalid tuntap mode %q", t.Mode)
	}

	var flags netlink.TuntapFlag

	if t.MultiQueue {
		flags = netlink.TUNTAP_MULTI_QUEUE_DEFAULTS
	} else {
		flags = netlink.TUNTAP_DEFAULTS
	}

	tuntap := &netlink.Tuntap{
		LinkAttrs: netlink.LinkAttrs{
			Name: t.Name,
		},
		Mode:  mode,
		Flags: flags,
	}

	err := netlink.LinkAdd(tuntap)
	if err != nil {
		return fmt.Errorf("Failed to create tuntap %q: %w", t.Name, err)
	}

	return nil
}
