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
		return fmt.Errorf("invalid tuntap mode %q", t.Mode)
	}

	// TODO: there is TUNTAP_DEFAULTS and TUNTAP_MULTI_QUEUE_DEFAULTS in the netlink package, I don't know if these should be used
	var flags netlink.TuntapFlag

	if t.MultiQueue {
		flags |= netlink.TUNTAP_MULTI_QUEUE
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
		return fmt.Errorf("failed to create tuntap %q: %w", t.Name, err)
	}

	return nil
}
