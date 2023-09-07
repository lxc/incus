//go:build linux && cgo

package idmap

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRawIdmap parses an IDMAP string.
func ParseRawIdmap(value string) ([]IdmapEntry, error) {
	getRange := func(r string) (int64, int64, error) {
		entries := strings.Split(r, "-")
		if len(entries) > 2 {
			return -1, -1, fmt.Errorf("Invalid ID Map range %q", r)
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

	ret := IdmapSet{}

	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			continue
		}

		entries := strings.Split(line, " ")
		if len(entries) != 3 {
			return nil, fmt.Errorf("Invalid ID map line %q", line)
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
			return nil, fmt.Errorf("The ID map ranges are of different sizes %q", line)
		}

		entry := IdmapEntry{
			Hostid:   outsideBase,
			Nsid:     insideBase,
			Maprange: insideSize,
		}

		switch entries[0] {
		case "both":
			entry.Isuid = true
			entry.Isgid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		case "uid":
			entry.Isuid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		case "gid":
			entry.Isgid = true
			err := ret.AddSafe(entry)
			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("Invalid ID map type %q", line)
		}
	}

	return ret.Idmap, nil
}
