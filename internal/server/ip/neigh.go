package ip

import (
	"bytes"
	"net"
	"strings"

	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
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

// Neigh represents arguments for neighbour manipulation.
type Neigh struct {
	DevName string
	Addr    net.IP
	MAC     net.HardwareAddr
	State   NeighbourIPState
}

// Show list neighbour entries filtered by DevName and optionally MAC address.
func (n *Neigh) Show() ([]Neigh, error) {
	out, err := subprocess.RunCommand("ip", "neigh", "show", "dev", n.DevName)
	if err != nil {
		return nil, err
	}

	neighbours := []Neigh{}

	for _, line := range util.SplitNTrimSpace(out, "\n", -1, true) {
		// Split fields and early validation.
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}

		addr := net.ParseIP(fields[0])
		if addr == nil {
			continue
		}

		mac, _ := net.ParseMAC(fields[2])

		// Check neighbour matches desired MAC address if specified.
		if n.MAC != nil {
			if !bytes.Equal(n.MAC, mac) {
				continue
			}
		}

		neighbours = append(neighbours, Neigh{
			Addr:  addr,
			MAC:   mac,
			State: NeighbourIPState(fields[3]),
		})
	}

	return neighbours, nil
}
