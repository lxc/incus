package ip

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// Addr represents arguments for address protocol manipulation.
type Addr struct {
	DevName string
	Address string
	Scope   string
	Family  string
}

// Add adds new protocol address.
func (a *Addr) Add() error {
	addr, err := netlink.ParseIPNet(a.Address)
	if err != nil {
		return err
	}

	scope, err := a.scopeNum()
	if err != nil {
		return err
	}

	err = netlink.AddrAdd(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: a.DevName,
		},
	}, &netlink.Addr{
		IPNet: addr,
		Scope: scope,
	})
	if err != nil {
		return fmt.Errorf("Failed to add address %v: %w", addr, err)
	}

	return nil
}

func (a *Addr) scopeNum() (int, error) {
	var scope netlink.Scope
	switch a.Scope {
	case "global", "universe", "":
		scope = netlink.SCOPE_UNIVERSE
	case "site":
		scope = netlink.SCOPE_SITE
	case "link":
		scope = netlink.SCOPE_LINK
	case "host":
		scope = netlink.SCOPE_HOST
	case "nowhere":
		scope = netlink.SCOPE_NOWHERE
	default:
		return 0, fmt.Errorf("Unknown address scope %q", a.Scope)
	}

	return int(scope), nil
}

// Flush flushes protocol addresses.
func (a *Addr) Flush() error {
	var family int
	switch a.Family {
	case FamilyV4:
		family = netlink.FAMILY_V4
	case FamilyV6:
		family = netlink.FAMILY_V6
	case "":
		family = netlink.FAMILY_ALL
	default:
		return fmt.Errorf("unknown address family %q", a.Family)
	}

	link, err := linkByName(a.DevName)
	if err != nil {
		return err
	}

	addrs, err := netlink.AddrList(link, family)
	if err != nil {
		return fmt.Errorf("Failed to get addresses for device %s: %w", a.DevName, err)
	}

	scope, err := a.scopeNum()
	if err != nil {
		return err
	}

	// NOTE: If this becomes a bottleneck, there appears to be support for batching those kind of changes within netlink.

	for _, addr := range addrs {
		if a.Scope != "" && scope != addr.Scope {
			continue
		}

		err := netlink.AddrDel(link, &addr)
		if err != nil {
			return fmt.Errorf("Failed to delete address %v: %w", addr, err)
		}
	}

	return nil
}
