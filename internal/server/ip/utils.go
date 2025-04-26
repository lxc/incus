package ip

import (
	"fmt"
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

// LinkInfo represents the IP link details.
type LinkInfo struct {
	InterfaceName    string
	Link             string
	Master           string
	Address          string
	TXQueueLength    uint32
	MTU              uint32
	OperationalState string
	Info             struct {
		Kind      string
		SlaveKind string
		Data      struct {
			Protocol string
			ID       int
		}
	}
}

func linkByName(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("Failed to get link %q: %w", name, err)
	}

	return link, nil
}

// GetLinkInfoByName returns the detailed information for the given link.
func GetLinkInfoByName(name string) (LinkInfo, error) {
	info := LinkInfo{}

	link, err := linkByName(name)
	if err != nil {
		return info, err
	}

	info.InterfaceName = link.Attrs().Name

	if link.Attrs().ParentIndex != 0 {
		parentLink, err := netlink.LinkByIndex(link.Attrs().ParentIndex)
		if err != nil {
			return info, fmt.Errorf("failed to get parent link %d of %q: %w", link.Attrs().ParentIndex, name, err)
		}

		info.Link = parentLink.Attrs().Name
	}

	if link.Attrs().MasterIndex != 0 {
		masterLink, err := netlink.LinkByIndex(link.Attrs().MasterIndex)
		if err != nil {
			return info, fmt.Errorf("failed to get master link %d of %q: %w", link.Attrs().ParentIndex, name, err)
		}

		info.Master = masterLink.Attrs().Name
	}

	info.Address = link.Attrs().HardwareAddr.String()
	info.TXQueueLength = uint32(link.Attrs().TxQLen)
	info.MTU = uint32(link.Attrs().MTU)
	info.OperationalState = link.Attrs().OperState.String()
	info.Info.Kind = link.Type()

	if link.Attrs().Slave != nil {
		info.Info.Kind = link.Attrs().Slave.SlaveType()
	}

	vlan, ok := link.(*netlink.Vlan)
	if ok {
		info.Info.Data.ID = vlan.VlanId
		info.Info.Data.Protocol = vlan.VlanProtocol.String()
	}

	return info, nil
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
