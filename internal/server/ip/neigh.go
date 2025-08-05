package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NeighbourIPState can be { NeighbourIPStatePermanent | NeighbourIPStateNoARP | NeighbourIPStateReachable | NeighbourIPStateStale | NeighbourIPStateNone | NeighbourIPStateIncomplete | NeighbourIPStateDelay | NeighbourIPStateProbe | NeighbourIPStateFailed }.
type NeighbourIPState int

const (
	// NeighbourIPStatePermanent the neighbour entry is valid forever and can be only be removed administratively.
	NeighbourIPStatePermanent NeighbourIPState = unix.NUD_PERMANENT

	// NeighbourIPStateNoARP the neighbour entry is valid. No attempts to validate this entry will be made but it can
	// be removed when its lifetime expires.
	NeighbourIPStateNoARP NeighbourIPState = unix.NUD_NOARP

	// NeighbourIPStateReachable the neighbour entry is valid until the reachability timeout expires.
	NeighbourIPStateReachable NeighbourIPState = unix.NUD_REACHABLE

	// NeighbourIPStateStale the neighbour entry is valid but suspicious.
	NeighbourIPStateStale NeighbourIPState = unix.NUD_STALE

	// NeighbourIPStateNone this is a pseudo state used when initially creating a neighbour entry or after trying to
	// remove it before it becomes free to do so.
	NeighbourIPStateNone NeighbourIPState = unix.NUD_NONE

	// NeighbourIPStateIncomplete the neighbour entry has not (yet) been validated/resolved.
	NeighbourIPStateIncomplete NeighbourIPState = unix.NUD_INCOMPLETE

	// NeighbourIPStateDelay neighbor entry validation is currently delayed.
	NeighbourIPStateDelay NeighbourIPState = unix.NUD_DELAY

	// NeighbourIPStateProbe neighbor is being probed.
	NeighbourIPStateProbe NeighbourIPState = unix.NUD_PROBE

	// NeighbourIPStateFailed max number of probes exceeded without success, neighbor validation has ultimately failed.
	NeighbourIPStateFailed NeighbourIPState = unix.NUD_FAILED
)

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
		return nil, fmt.Errorf("Failed to get neighbours for link %q: %w", n.DevName, err)
	}

	neighbours := make([]Neigh, 0, len(netlinkNeighbours))

	for _, neighbour := range netlinkNeighbours {
		if neighbour.HardwareAddr.String() != n.MAC.String() {
			continue
		}

		neighbours = append(neighbours, Neigh{
			Addr:  neighbour.IP,
			MAC:   neighbour.HardwareAddr,
			State: NeighbourIPState(neighbour.State),
		})
	}

	return neighbours, nil
}
