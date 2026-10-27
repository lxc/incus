package ip

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NeighborIPState can be { NeighborIPStatePermanent | NeighborIPStateNoARP | NeighborIPStateReachable | NeighborIPStateStale | NeighborIPStateNone | NeighborIPStateIncomplete | NeighborIPStateDelay | NeighborIPStateProbe | NeighborIPStateFailed }.
type NeighborIPState int

const (
	// NeighborIPStatePermanent the neighbor entry is valid forever and can be only be removed administratively.
	NeighborIPStatePermanent NeighborIPState = unix.NUD_PERMANENT

	// NeighborIPStateNoARP the neighbor entry is valid. No attempts to validate this entry will be made but it can
	// be removed when its lifetime expires.
	NeighborIPStateNoARP NeighborIPState = unix.NUD_NOARP

	// NeighborIPStateReachable the neighbor entry is valid until the reachability timeout expires.
	NeighborIPStateReachable NeighborIPState = unix.NUD_REACHABLE

	// NeighborIPStateStale the neighbor entry is valid but suspicious.
	NeighborIPStateStale NeighborIPState = unix.NUD_STALE

	// NeighborIPStateNone this is a pseudo state used when initially creating a neighbor entry or after trying to
	// remove it before it becomes free to do so.
	NeighborIPStateNone NeighborIPState = unix.NUD_NONE

	// NeighborIPStateIncomplete the neighbor entry has not (yet) been validated/resolved.
	NeighborIPStateIncomplete NeighborIPState = unix.NUD_INCOMPLETE

	// NeighborIPStateDelay neighbor entry validation is currently delayed.
	NeighborIPStateDelay NeighborIPState = unix.NUD_DELAY

	// NeighborIPStateProbe neighbor is being probed.
	NeighborIPStateProbe NeighborIPState = unix.NUD_PROBE

	// NeighborIPStateFailed max number of probes exceeded without success, neighbor validation has ultimately failed.
	NeighborIPStateFailed NeighborIPState = unix.NUD_FAILED
)

// Neigh represents arguments for neighbor manipulation.
type Neigh struct {
	DevName string
	Addr    net.IP
	MAC     net.HardwareAddr
	State   NeighborIPState
}

// Show list neighbor entries filtered by DevName and optionally MAC address.
func (n *Neigh) Show() ([]Neigh, error) {
	link, err := linkByName(n.DevName)
	if err != nil {
		return nil, err
	}

	netlinkNeighbors, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("Failed to get neighbors for link %q: %w", n.DevName, err)
	}

	neighbors := make([]Neigh, 0, len(netlinkNeighbors))

	for _, neighbor := range netlinkNeighbors {
		if neighbor.HardwareAddr.String() != n.MAC.String() {
			continue
		}

		neighbors = append(neighbors, Neigh{
			Addr:  neighbor.IP,
			MAC:   neighbor.HardwareAddr,
			State: NeighborIPState(neighbor.State),
		})
	}

	return neighbors, nil
}
