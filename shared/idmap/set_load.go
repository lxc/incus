package idmap

import (
	"fmt"
	"strconv"
	"strings"
)

// NewSetFromIncusIDMap parses an Incus raw.idmap into a new idmap Set.
func NewSetFromIncusIDMap(value string) (*Set, error) {
	getRange := func(r string) (int64, int64, error) {
		entries := strings.Split(r, "-")
		if len(entries) > 2 {
			return -1, -1, fmt.Errorf("Invalid ID map range: %s", r)
		}

		base, err := strconv.ParseInt(entries[0], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		size := int64(1)
		if len(entries) > 1 {
			size, err = strconv.ParseInt(entries[1], 10, 64)
			if err != nil {
				return -1, -1, err
			}

			size -= base
			size++
		}

		return base, size, nil
	}

	ret := &Set{}

	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			continue
		}

		entries := strings.Split(line, " ")
		if len(entries) != 3 {
			return nil, fmt.Errorf("Invalid ID map line: %s", line)
		}

		outsideBase, outsideSize, err := getRange(entries[1])
		if err != nil {
			return nil, err
		}

		insideBase, insideSize, err := getRange(entries[2])
		if err != nil {
			return nil, err
		}

		if insideSize != outsideSize {
			return nil, fmt.Errorf("The ID map ranges are of different sizes: %s", line)
		}

		entry := Entry{
			HostID:   outsideBase,
			NSID:     insideBase,
			MapRange: insideSize,
		}

		switch entries[0] {
		case "both":
			entry.IsUID = true
			entry.IsGID = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		case "uid":
			entry.IsUID = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		case "gid":
			entry.IsGID = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("Invalid ID map type: %s", line)
		}
	}

	return ret, nil
}
