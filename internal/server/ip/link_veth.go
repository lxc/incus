package ip

import (
	"github.com/vishvananda/netlink"
)

// Veth represents arguments for link of type veth.
type Veth struct {
	Link
	Peer Link
}

// Add adds new virtual link.
func (veth *Veth) Add() error {
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

	return veth.addLink(link)
}
