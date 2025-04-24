package ip

import (
	"github.com/vishvananda/netlink"
)

// Veth represents arguments for link of type veth.
type Veth struct {
	Link
	Peer   Link
	Master string // TODO: Link already has a master, and the way this one is used does not suggest that it is different
}

// Add adds new virtual link.
func (veth *Veth) Add() error {
	veth.Link.Master = veth.Master // TODO: this is just to see if it works
	attrs, err := veth.netlinkAttrs()
	if err != nil {
		return err
	}

	link := netlink.NewVeth(attrs)

	peerAttrs, err := veth.Peer.netlinkAttrs()
	if err != nil {
		return err
	}

	link.PeerMTU = uint32(peerAttrs.MTU)
	link.PeerName = peerAttrs.Name
	link.PeerNamespace = peerAttrs.Namespace
	link.PeerNumTxQueues = uint32(peerAttrs.NumTxQueues)
	link.PeerNumRxQueues = uint32(peerAttrs.NumRxQueues)
	link.PeerTxQLen = peerAttrs.TxQLen
	link.PeerHardwareAddr = peerAttrs.HardwareAddr

	return netlink.LinkAdd(link)
}
