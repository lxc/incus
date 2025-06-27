package ip

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Family can be { FamilyAll, FamilyV4, FamilyV6 }.
type Family int

const (
	// FamilyAll specifies any/all family.
	FamilyAll Family = unix.AF_UNSPEC

	// FamilyV4 specifies the IPv4 family.
	FamilyV4 Family = unix.AF_INET

	// FamilyV6 specifies the IPv6 family.
	FamilyV6 Family = unix.AF_INET6
)

func linkByName(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("Failed to get link %q: %w", name, err)
	}

	return link, nil
}

func parseHandle(id string) (uint32, error) {
	if id == "root" {
		return netlink.HANDLE_ROOT, nil
	}

	majorStr, minorStr, found := strings.Cut(id, ":")

	if !found {
		return 0, fmt.Errorf("Invalid handle %q", id)
	}

	major, err := strconv.ParseUint(majorStr, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("Invalid handle %q: %w", id, err)
	}

	minor, err := strconv.ParseUint(minorStr, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("Invalid handle %q: %w", id, err)
	}

	return netlink.MakeHandle(uint16(major), uint16(minor)), nil
}

// ParseIPNet parses a CIDR string and returns a *net.IPNet containing both the full address and the netmask,
// Unlike net.ParseCIDR which zeroes out the host part in the returned *net.IPNet and returns the full address separately.
func ParseIPNet(addr string) (*net.IPNet, error) {
	return netlink.ParseIPNet(addr)
}
