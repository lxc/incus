package drivers

import (
	"encoding/hex"
	"fmt"
	"net"
)

// portRangesFromSlice checks if adjacent indices in the given slice contain consecutive
// numbers and returns a slice of port ranges ([startNumber, rangeSize]) accordingly.
//
// Note that this function cannot differentiate ranges from adjacent ports e.g. if the given
// slice is "[80,81,82]" then the returned range will be "80-82", regardless of whether the
// user input was parsed from "80-82" or "80,81,82".
func portRangesFromSlice(ports []uint64) [][2]uint64 {
	if len(ports) == 0 {
		return nil
	}

	portRanges := make([][2]uint64, 0, len(ports))
	startIdx := 0
	size := uint64(0)
	for i := range ports {
		if i == len(ports)-1 || ports[i+1] != ports[i]+1 {
			size = ports[i] - ports[startIdx] + 1
			portRanges = append(portRanges, [2]uint64{ports[startIdx], size})
			startIdx = i + 1
		}
	}

	return portRanges
}

func portRangeStr(portRange [2]uint64, delimiter string) string {
	if portRange[1] < 1 {
		return ""
	} else if portRange[1] == 1 {
		return fmt.Sprintf("%d", portRange[0])
	}

	return fmt.Sprintf("%d%s%d", portRange[0], delimiter, portRange[0]+portRange[1]-1)
}

// getOptimisedDNATRanges returns a map of listen port ranges to target port ranges that can be
// applied in any order.
//
// Both Xtables and Nftables are able to apply rules for multiple listen ports at a time when a
// listen port range exactly matches the corresponding target port range (e.g. "80-85" to "80-85")
// or when there is a single target port (e.g. "80-85" to "80"). This function checks when these
// conditions are met and returns a map of listen and target port ranges to be applied by the loaded
// driver.
func getOptimisedDNATRanges(forward *AddressForward) map[[2]uint64][2]uint64 {
	targetPortsLen := len(forward.TargetPorts)
	listenPortsLen := len(forward.ListenPorts)

	snatRules := make(map[[2]uint64][2]uint64, listenPortsLen)
	listenPortRanges := portRangesFromSlice(forward.ListenPorts)

	// If there is only one target port, DNAT rules can be optimised for all listen ranges.
	if targetPortsLen == 1 {
		targetPort := forward.TargetPorts[0]
		for _, listenPortRange := range listenPortRanges {
			snatRules[listenPortRange] = [2]uint64{targetPort, 1}
		}

		return snatRules
	}

	// For a given listen range, the corresponding target range may not simply be targetRange[i] (where "i"
	// is the index of the listen range). For example, "100-101,300" to "100-102" would be valid config
	// because the number of listen and target ports are equal, but there are two listen ranges and one
	// target range. Instead, to check if there is a target range we create a map of port range starting
	// values and check if the current target port is in the map.
	targetPortRanges := portRangesFromSlice(forward.TargetPorts)
	targetPortRangeMap := make(map[uint64]uint64, len(targetPortRanges))
	for _, targetPortRange := range targetPortRanges {
		targetPortRangeMap[targetPortRange[0]] = targetPortRange[1]
	}

	nProcessedPorts := 0
	for _, listenPortRange := range listenPortRanges {
		rangeEndIdx := nProcessedPorts + int(listenPortRange[1])

		currentTargetPort := forward.TargetPorts[nProcessedPorts]
		targetPortRangeSize, ok := targetPortRangeMap[currentTargetPort]

		// Check that we have a target port range and that the listen and target port ranges start
		// at the same value.
		if ok && listenPortRange[0] == currentTargetPort {
			targetPortRange := [2]uint64{currentTargetPort, targetPortRangeSize}

			// Check if the listen and target ranges are the same size.
			if listenPortRange[1] == targetPortRangeSize {
				// Port ranges are identical. One to one mapping.
				snatRules[listenPortRange] = targetPortRange
				nProcessedPorts += int(listenPortRange[1])
			} else {
				// Port ranges are identical until the end of the target range.
				// Rules can be optimised for a portion of ports in the listen range.
				snatRules[[2]uint64{listenPortRange[0], targetPortRangeSize}] = targetPortRange
				nProcessedPorts += int(targetPortRangeSize)
			}
		}

		// Remaining ports in the listen range cannot be optimised.
		for ; nProcessedPorts < rangeEndIdx; nProcessedPorts++ {
			snatRules[[2]uint64{forward.ListenPorts[nProcessedPorts], 1}] = [2]uint64{forward.TargetPorts[nProcessedPorts], 1}
		}
	}

	return snatRules
}

// subnetMask returns the subnet mask of the given network as a string. Both IPv4 and IPv6 are handled.
func subnetMask(ipNet *net.IPNet) string {
	if ipNet.IP.To4() != nil {
		return fmt.Sprintf("%d.%d.%d.%d", ipNet.Mask[0], ipNet.Mask[1], ipNet.Mask[2], ipNet.Mask[3])
	}

	var hexMask []rune
	for i, r := range ipNet.Mask.String() {
		if i%4 == 0 && i != 0 {
			hexMask = append(hexMask, ':')
		}

		hexMask = append(hexMask, r)
	}

	// Shorten into canonical form.
	return net.ParseIP(string(hexMask)).String()
}

// subnetPrefixHex returns the hex string which prefixes a subnet (e.g. the hex prefix of "fd25:c7e3:5dec:e4dd:ef14::1/64"
// is "fd25c7e35dece4dd"). Only for use with IPv6 networks.
func subnetPrefixHex(ipNet *net.IPNet) (string, error) {
	if ipNet == nil || ipNet.IP.To4() != nil {
		return "", fmt.Errorf("Cannot create a hex prefix for empty or IPv4 subnets")
	}

	hexStr := hex.EncodeToString(ipNet.IP)
	ones, _ := ipNet.Mask.Size()
	if ones%8 != 0 {
		return "", fmt.Errorf("Cannot create a hex prefix for an IPv6 subnet whose CIDR range is not divisible by 8")
	}

	return hexStr[:ones/4], nil
}
