package ip

import (
	"fmt"
	"net"
	"strconv"

	"github.com/vishvananda/netlink"
)

// Link represents base arguments for link device.
type Link struct {
	Name          string
	Kind          string
	MTU           uint32
	Parent        string
	Address       net.HardwareAddr
	TXQueueLength uint32
	AllMulticast  bool
	Master        string
	Up            bool
}

// LinkInfo has additional information about a link.
type LinkInfo struct {
	Link
	OperationalState string
	SlaveKind        string
	VlanID           int
}

func (l *Link) netlinkAttrs() (netlink.LinkAttrs, error) {
	linkAttrs := netlink.NewLinkAttrs()

	linkAttrs.Name = l.Name

	if l.MTU != 0 {
		linkAttrs.MTU = int(l.MTU)
	}

	if l.Address != nil {
		linkAttrs.HardwareAddr = l.Address
	}

	if l.TXQueueLength != 0 {
		linkAttrs.TxQLen = int(l.TXQueueLength)
	}

	if l.Parent != "" {
		parentLink, err := linkByName(l.Parent)
		if err != nil {
			return netlink.LinkAttrs{}, err
		}

		linkAttrs.ParentIndex = parentLink.Attrs().Index
	}

	if l.Master != "" {
		masterLink, err := linkByName(l.Master)
		if err != nil {
			return netlink.LinkAttrs{}, err
		}

		linkAttrs.MasterIndex = masterLink.Attrs().Index
	}

	if l.Up {
		linkAttrs.Flags |= net.FlagUp
	}

	return linkAttrs, nil
}

func (l *Link) addLink(link netlink.Link) error {
	err := netlink.LinkAdd(link)
	if err != nil {
		return err
	}

	// ALLMULTI can't be set on create
	err = l.SetAllMulticast(l.AllMulticast)
	if err != nil {
		return err
	}

	return nil
}

// LinkByName returns a Link from a device name.
func LinkByName(name string) (LinkInfo, error) {
	link, err := linkByName(name)
	if err != nil {
		return LinkInfo{}, err
	}

	var parent, master string

	if link.Attrs().ParentIndex != 0 {
		parentLink, err := netlink.LinkByIndex(link.Attrs().ParentIndex)
		if err != nil {
			return LinkInfo{}, err
		}

		parent = parentLink.Attrs().Name
	}

	if link.Attrs().MasterIndex != 0 {
		masterLink, err := netlink.LinkByIndex(link.Attrs().MasterIndex)
		if err != nil {
			return LinkInfo{}, err
		}

		master = masterLink.Attrs().Name
	}

	var vlanID int
	vlan, ok := link.(*netlink.Vlan)
	if ok {
		vlanID = vlan.VlanId
	}

	return LinkInfo{
		Link: Link{
			Name:          link.Attrs().Name,
			Kind:          link.Type(),
			MTU:           uint32(link.Attrs().MTU),
			Parent:        parent,
			Address:       link.Attrs().HardwareAddr,
			TXQueueLength: uint32(link.Attrs().TxQLen),
			AllMulticast:  link.Attrs().Allmulti == 1,
			Master:        master,
			Up:            (link.Attrs().Flags & net.FlagUp) != 0,
		},
		OperationalState: link.Attrs().OperState.String(),
		VlanID:           vlanID,
	}, nil
}

// SetUp enables the link device.
func (l *Link) SetUp() error {
	return netlink.LinkSetUp(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	})
}

// SetDown disables the link device.
func (l *Link) SetDown() error {
	return netlink.LinkSetDown(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	})
}

// SetMTU sets the MTU of the link device.
func (l *Link) SetMTU(mtu uint32) error {
	return netlink.LinkSetMTU(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, int(mtu))
}

// SetTXQueueLength sets the txqueuelen of the link device.
func (l *Link) SetTXQueueLength(queueLength uint32) error {
	return netlink.LinkSetTxQLen(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, int(queueLength))
}

// SetAddress sets the address of the link device.
func (l *Link) SetAddress(address net.HardwareAddr) error {
	return netlink.LinkSetHardwareAddr(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, address)
}

// SetAllMulticast when enabled instructs network driver to retrieve all multicast packets from the network to the
// kernel for further processing.
func (l *Link) SetAllMulticast(enabled bool) error {
	link := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}

	if enabled {
		return netlink.LinkSetAllmulticastOn(link)
	}

	return netlink.LinkSetAllmulticastOff(link)
}

// SetMaster sets the master of the link device.
func (l *Link) SetMaster(master string) error {
	return netlink.LinkSetMaster(
		&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: l.Name,
			},
		},
		&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: master,
			},
		},
	)
}

// SetNoMaster removes the master of the link device.
func (l *Link) SetNoMaster() error {
	return netlink.LinkSetNoMaster(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	})
}

// SetName sets the name of the link device.
func (l *Link) SetName(newName string) error {
	return netlink.LinkSetName(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, newName)
}

// SetNetns moves the link to the selected network namespace.
func (l *Link) SetNetns(netnsPid string) error {
	pid, err := strconv.Atoi(netnsPid)
	if err != nil {
		return err
	}

	return netlink.LinkSetNsPid(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, pid)
}

// SetVfAddress changes the address for the specified vf.
func (l *Link) SetVfAddress(vf string, address string) error {
	vfInt, err := strconv.Atoi(vf)
	if err != nil {
		return err
	}

	hwAddress, err := net.ParseMAC(address)
	if err != nil {
		return err
	}

	return netlink.LinkSetVfHardwareAddr(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, vfInt, hwAddress)
}

// SetVfVlan changes the assigned VLAN for the specified vf.
func (l *Link) SetVfVlan(vf string, vlan string) error {
	vfInt, err := strconv.Atoi(vf)
	if err != nil {
		return err
	}

	vlanInt, err := strconv.Atoi(vlan)
	if err != nil {
		return err
	}

	return netlink.LinkSetVfVlan(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, vfInt, vlanInt)
}

// SetVfSpoofchk turns packet spoof checking on or off for the specified VF.
func (l *Link) SetVfSpoofchk(vf string, on bool) error {
	vfInt, err := strconv.Atoi(vf)
	if err != nil {
		return err
	}

	return netlink.LinkSetVfSpoofchk(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, vfInt, on)
}

// VirtFuncInfo holds information about vf.
type VirtFuncInfo struct {
	VF         int
	Address    net.HardwareAddr
	VLAN       int
	SpoofCheck bool
}

// GetVFInfo returns info about virtual function.
func (l *Link) GetVFInfo(vfID int) (VirtFuncInfo, error) {
	link, err := linkByName(l.Name)
	if err != nil {
		return VirtFuncInfo{}, err
	}

	for _, vf := range link.Attrs().Vfs {
		if vf.ID == vfID {
			return VirtFuncInfo{
				VF:         vfID,
				Address:    vf.Mac,
				VLAN:       vf.Vlan,
				SpoofCheck: vf.Spoofchk,
			}, nil
		}
	}

	return VirtFuncInfo{}, fmt.Errorf("No matching virtual function found")
}

// Delete deletes the link device.
func (l *Link) Delete() error {
	return netlink.LinkDel(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	})
}

// BridgeVLANAdd adds a new vlan filter entry.
func (l *Link) BridgeVLANAdd(vid string, pvid bool, untagged bool, self bool) error {
	vidInt, err := strconv.Atoi(vid)
	if err != nil {
		return err
	}

	return netlink.BridgeVlanAdd(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, uint16(vidInt), pvid, untagged, self, !self)
}

// BridgeVLANDelete removes an existing vlan filter entry.
func (l *Link) BridgeVLANDelete(vid string, self bool) error {
	vidInt, err := strconv.Atoi(vid)
	if err != nil {
		return err
	}

	return netlink.BridgeVlanDel(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, uint16(vidInt), false, false, self, !self)
}

// BridgeLinkSetIsolated sets bridge 'isolated' attribute on a port.
func (l *Link) BridgeLinkSetIsolated(isolated bool) error {
	return netlink.LinkSetIsolated(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, isolated)
}

// BridgeLinkSetHairpin sets bridge 'hairpin' attribute on a port.
func (l *Link) BridgeLinkSetHairpin(hairpin bool) error {
	return netlink.LinkSetHairpin(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Name: l.Name,
		},
	}, hairpin)
}
