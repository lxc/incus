package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NeighProxy represents arguments for neighbor proxy manipulation.
type NeighProxy struct {
	DevName string
	Addr    net.IP
}

// Show list neighbor proxy entries.
func (n *NeighProxy) Show() ([]NeighProxy, error) {
	link, err := linkByName(n.DevName)
	if err != nil {
		return nil, err
	}

	list, err := netlink.NeighProxyList(link.Attrs().Index, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("Failed to get neighbor proxies for link %q: %w", n.DevName, err)
	}

	entries := make([]NeighProxy, 0, len(list))

	for _, neigh := range list {
		entries = append(entries, NeighProxy{
			DevName: n.DevName,
			Addr:    neigh.IP,
		})
	}

	return entries, nil
}

func (n *NeighProxy) netlinkNeigh() (*netlink.Neigh, error) {
	link, err := linkByName(n.DevName)
	if err != nil {
		return nil, err
	}

	return &netlink.Neigh{
		LinkIndex: link.Attrs().Index,
		Flags:     unix.NTF_PROXY,
		State:     unix.NUD_PERMANENT,
		IP:        n.Addr,
	}, nil
}

// Add a neighbor proxy entry.
func (n *NeighProxy) Add() error {
	neigh, err := n.netlinkNeigh()
	if err != nil {
		return err
	}

	err = netlink.NeighAdd(neigh)
	if err != nil {
		return fmt.Errorf("Failed to add neighbor proxy %v: %w", neigh, err)
	}

	return nil
}

// Delete a neighbor proxy entry.
func (n *NeighProxy) Delete() error {
	neigh, err := n.netlinkNeigh()
	if err != nil {
		return err
	}

	err = netlink.NeighDel(neigh)
	if err != nil {
		return fmt.Errorf("Failed to delete neighbor proxy %v: %w", neigh, err)
	}

	return nil
}
