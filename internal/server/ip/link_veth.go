package ip

import (
	"github.com/lxc/incus/v6/shared/subprocess"
)

// Veth represents arguments for link of type veth.
type Veth struct {
	Link
	Peer   Link
	Master string // TODO: Link already has a master, and the way this one is used does not suggest that it is different
}

// Add adds new virtual link.
func (veth *Veth) Add() error {
	// TODO: blocked on https://github.com/vishvananda/netlink/pull/1079
	err := veth.Link.add("veth", append([]string{"peer"}, veth.Peer.args()...))
	if err != nil {
		return err
	}

	if veth.Master != "" {
		_, err := subprocess.RunCommand("ip", "link", "set", veth.Name, "master", veth.Master)
		if err != nil {
			return err
		}
	}

	return nil
}
