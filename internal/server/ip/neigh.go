package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

// NeighbourIPState can be { PERMANENT | NOARP | REACHABLE | STALE | NONE | INCOMPLETE | DELAY | PROBE | FAILED }.
type NeighbourIPState string

// NeighbourIPStatePermanent the neighbour entry is valid forever and can be only be removed administratively.
const NeighbourIPStatePermanent = "PERMANENT"

// NeighbourIPStateNoARP the neighbour entry is valid. No attempts to validate this entry will be made but it can
// be removed when its lifetime expires.
const NeighbourIPStateNoARP = "NOARP"

// NeighbourIPStateReachable the neighbour entry is valid until the reachability timeout expires.
const NeighbourIPStateReachable = "REACHABLE"

// NeighbourIPStateStale the neighbour entry is valid but suspicious.
const NeighbourIPStateStale = "STALE"

// NeighbourIPStateNone this is a pseudo state used when initially creating a neighbour entry or after trying to
// remove it before it becomes free to do so.
const NeighbourIPStateNone = "NONE"

// NeighbourIPStateIncomplete the neighbour entry has not (yet) been validated/resolved.
const NeighbourIPStateIncomplete = "INCOMPLETE"

// NeighbourIPStateDelay neighbor entry validation is currently delayed.
const NeighbourIPStateDelay = "DELAY"

// NeighbourIPStateProbe neighbor is being probed.
const NeighbourIPStateProbe = "PROBE"

// NeighbourIPStateFailed max number of probes exceeded without success, neighbor validation has ultimately failed.
const NeighbourIPStateFailed = "FAILED"

func mapNetlinkState(state int) NeighbourIPState {
	switch state {
	case netlink.NUD_NONE:
		return NeighbourIPStateNone
	case netlink.NUD_INCOMPLETE:
		return NeighbourIPStateIncomplete
	case netlink.NUD_REACHABLE:
		return NeighbourIPStateReachable
	case netlink.NUD_STALE:
		return NeighbourIPStateStale
	case netlink.NUD_DELAY:
		return NeighbourIPStateDelay
	case netlink.NUD_PROBE:
		return NeighbourIPStateProbe
	case netlink.NUD_FAILED:
		return NeighbourIPStateFailed
	case netlink.NUD_NOARP:
		return NeighbourIPStateNoARP
	case netlink.NUD_PERMANENT:
		return NeighbourIPStatePermanent
	default:
		return NeighbourIPStateNone
	}
}

// Neigh represents arguments for neighbour manipulation.
type Neigh struct {
	DevName string
	Addr    net.IP
	MAC     net.HardwareAddr
	State   NeighbourIPState
}

// Show list neighbour entries filtered by DevName and optionally MAC address.
func (n *Neigh) Show() ([]Neigh, error) {
	link, err := linkByName(n.DevName)
	if err != nil {
		return nil, err
	}

	netlinkNeighbours, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("failed to get neighbours for link %q: %w", n.DevName, err)
	}

	neighbours := make([]Neigh, 0, len(netlinkNeighbours))

	for _, neighbour := range netlinkNeighbours {
		neighbours = append(neighbours, Neigh{
			Addr:  neighbour.IP,
			MAC:   neighbour.HardwareAddr,
			State: mapNetlinkState(neighbour.State),
		})
	}

	return neighbours, nil
}
