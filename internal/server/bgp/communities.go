package bgp

import (
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/shared/validate"
)

// ParseCommunities parses a comma separated list of standard communities (ASN:value).
func ParseCommunities(value string) ([]uint32, error) {
	communities := []uint32{}
	for entry := range strings.SplitSeq(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		err := validate.IsBGPCommunity(entry)
		if err != nil {
			return nil, err
		}

		asn, val, _ := strings.Cut(entry, ":")

		// Both parts were validated above.
		asnInt, _ := strconv.ParseUint(asn, 10, 16)
		valInt, _ := strconv.ParseUint(val, 10, 16)

		community := uint32(asnInt)<<16 | uint32(valInt)
		if !slices.Contains(communities, community) {
			communities = append(communities, community)
		}
	}

	return communities, nil
}
