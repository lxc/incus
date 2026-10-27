package ip

import (
	"net"

	"github.com/vishvananda/netlink"
)

// Gretap represents arguments for link of type gretap.
type Gretap struct {
	Link
	Local  net.IP
	Remote net.IP
}

// Add adds new virtual link.
func (g *Gretap) Add() error {
	attrs, err := g.netlinkAttrs()
	if err != nil {
		return err
	}

	return g.addLink(&netlink.Gretap{
		LinkAttrs: attrs,
		Local:     g.Local,
		Remote:    g.Remote,
	})
}
