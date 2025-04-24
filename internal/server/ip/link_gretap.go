package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

// Gretap represents arguments for link of type gretap.
type Gretap struct {
	Link
	Local  string
	Remote string
}

// Add adds new virtual link.
func (g *Gretap) Add() error {
	attrs, err := g.netlinkAttrs()
	if err != nil {
		return err
	}

	local := net.ParseIP(g.Local)
	if local == nil {
		return fmt.Errorf("Invalid local address %q", g.Local)
	}

	remote := net.ParseIP(g.Remote)
	if remote == nil {
		return fmt.Errorf("Invalid remote address %q", g.Remote)
	}

	return netlink.LinkAdd(&netlink.Gretap{
		LinkAttrs: attrs,
		Local:     local,
		Remote:    remote,
	})
}
