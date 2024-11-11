//go:build linux

package linux

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// NetlinkInterface returns a net.Interface extended to also contain its addresses.
type NetlinkInterface struct {
	net.Interface

	Addresses []net.Addr
}

// NetlinkInterfaces performs a RTM_GETADDR call to get both.
func NetlinkInterfaces() ([]NetlinkInterface, error) {
	// Grab the interface list.
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	// Initialize result slice.
	netlinkIfaces := make([]NetlinkInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		netlinkIfaces = append(netlinkIfaces, NetlinkInterface{iface, make([]net.Addr, 0)})
	}

	// Turn it into a map.
	ifaceMap := make(map[int]*NetlinkInterface, len(ifaces))
	for k, v := range netlinkIfaces {
		ifaceMap[v.Index] = &netlinkIfaces[k] //nolint:typecheck
	}

	// Make the netlink call.
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return nil, fmt.Errorf("Failed to query RTM_GETADDR: %v", err)
	}

	messages, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse RTM_GETADDR: %v", err)
	}

	for _, m := range messages {
		if m.Header.Type == syscall.RTM_NEWADDR {
			addrMessage := (*syscall.IfAddrmsg)(unsafe.Pointer(&m.Data[0]))

			addrAttrs, err := syscall.ParseNetlinkRouteAttr(&m)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse route attribute: %v", err)
			}

			ifi, ok := ifaceMap[int(addrMessage.Index)]
			if ok {
				ifi.Addresses = append(ifi.Addresses, newAddr(addrMessage, addrAttrs))
			}
		}
	}

	return netlinkIfaces, nil
}

// Variation of function of the same name from within Go source.
func newAddr(ifam *syscall.IfAddrmsg, attrs []syscall.NetlinkRouteAttr) net.Addr {
	var ipPointToPoint bool

	// Seems like we need to make sure whether the IP interface
	// stack consists of IP point-to-point numbered or unnumbered
	// addressing.
	for _, a := range attrs {
		if a.Attr.Type == syscall.IFA_LOCAL {
			ipPointToPoint = true
			break
		}
	}

	for _, a := range attrs {
		if ipPointToPoint && a.Attr.Type == syscall.IFA_ADDRESS {
			continue
		}

		switch ifam.Family {
		case syscall.AF_INET:
			return &net.IPNet{IP: net.IPv4(a.Value[0], a.Value[1], a.Value[2], a.Value[3]), Mask: net.CIDRMask(int(ifam.Prefixlen), 8*net.IPv4len)}
		case syscall.AF_INET6:
			ifa := &net.IPNet{IP: make(net.IP, net.IPv6len), Mask: net.CIDRMask(int(ifam.Prefixlen), 8*net.IPv6len)}
			copy(ifa.IP, a.Value[:])
			return ifa
		}
	}

	return nil
}
