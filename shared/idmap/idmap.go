package idmap

import (
	"fmt"
	"strconv"
	"strings"
)

// IdmapEntry is a single idmap entry (line).
type IdmapEntry struct {
	Isuid    bool
	Isgid    bool
	Hostid   int64 // id as seen on the host - i.e. 100000
	Nsid     int64 // id as seen in the ns - i.e. 0
	Maprange int64
}

func (e *IdmapEntry) ToLxcString() []string {
	if e.Isuid && e.Isgid {
		return []string{
			fmt.Sprintf("u %d %d %d", e.Nsid, e.Hostid, e.Maprange),
			fmt.Sprintf("g %d %d %d", e.Nsid, e.Hostid, e.Maprange),
		}
	}

	if e.Isuid {
		return []string{fmt.Sprintf("u %d %d %d", e.Nsid, e.Hostid, e.Maprange)}
	}

	return []string{fmt.Sprintf("g %d %d %d", e.Nsid, e.Hostid, e.Maprange)}
}

func (e *IdmapEntry) HostidsIntersect(i IdmapEntry) bool {
	if (e.Isuid && i.Isuid) || (e.Isgid && i.Isgid) {
		switch {
		case is_between(e.Hostid, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid, e.Hostid, e.Hostid+e.Maprange):
			return true
		case is_between(e.Hostid+e.Maprange, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid+i.Maprange, e.Hostid, e.Hostid+e.Maprange):
			return true
		}
	}

	return false
}

func (e *IdmapEntry) Intersects(i IdmapEntry) bool {
	if (e.Isuid && i.Isuid) || (e.Isgid && i.Isgid) {
		switch {
		case is_between(e.Hostid, i.Hostid, i.Hostid+i.Maprange-1):
			return true
		case is_between(i.Hostid, e.Hostid, e.Hostid+e.Maprange-1):
			return true
		case is_between(e.Hostid+e.Maprange-1, i.Hostid, i.Hostid+i.Maprange-1):
			return true
		case is_between(i.Hostid+i.Maprange-1, e.Hostid, e.Hostid+e.Maprange-1):
			return true
		case is_between(e.Nsid, i.Nsid, i.Nsid+i.Maprange-1):
			return true
		case is_between(i.Nsid, e.Nsid, e.Nsid+e.Maprange-1):
			return true
		case is_between(e.Nsid+e.Maprange-1, i.Nsid, i.Nsid+i.Maprange-1):
			return true
		case is_between(i.Nsid+i.Maprange-1, e.Nsid, e.Nsid+e.Maprange-1):
			return true
		}
	}
	return false
}

// HostIDsCoveredBy returns whether or not the entry is covered by the supplied host UID and GID ID maps.
// If e.Isuid is true then host IDs must be covered by an entry in allowedHostUIDs, and if e.Isgid is true then
// host IDs must be covered by an entry in allowedHostGIDs.
func (e *IdmapEntry) HostIDsCoveredBy(allowedHostUIDs []IdmapEntry, allowedHostGIDs []IdmapEntry) bool {
	if !e.Isuid && !e.Isgid {
		return false // This is an invalid idmap entry.
	}

	isUIDAllowed := false

	if e.Isuid {
		for _, allowedIDMap := range allowedHostUIDs {
			if !allowedIDMap.Isuid {
				continue
			}

			if e.Hostid >= allowedIDMap.Hostid && (e.Hostid+e.Maprange) <= (allowedIDMap.Hostid+allowedIDMap.Maprange) {
				isUIDAllowed = true
				break
			}
		}
	}

	isGIDAllowed := false

	if e.Isgid {
		for _, allowedIDMap := range allowedHostGIDs {
			if !allowedIDMap.Isgid {
				continue
			}

			if e.Hostid >= allowedIDMap.Hostid && (e.Hostid+e.Maprange) <= (allowedIDMap.Hostid+allowedIDMap.Maprange) {
				isGIDAllowed = true
				break
			}
		}
	}

	return e.Isuid == isUIDAllowed && e.Isgid == isGIDAllowed
}

func (e *IdmapEntry) Usable() error {
	kernelIdmap, err := CurrentIdmapSet()
	if err != nil {
		return err
	}

	kernelRanges, err := kernelIdmap.ValidRanges()
	if err != nil {
		return err
	}

	// Validate the uid map
	if e.Isuid {
		valid := false
		for _, kernelRange := range kernelRanges {
			if !kernelRange.Isuid {
				continue
			}

			if kernelRange.Contains(e.Hostid) && kernelRange.Contains(e.Hostid+e.Maprange-1) {
				valid = true
				break
			}
		}

		if !valid {
			return fmt.Errorf("The '%s' map can't work in the current user namespace", e.ToLxcString())
		}
	}

	// Validate the gid map
	if e.Isgid {
		valid := false
		for _, kernelRange := range kernelRanges {
			if !kernelRange.Isgid {
				continue
			}

			if kernelRange.Contains(e.Hostid) && kernelRange.Contains(e.Hostid+e.Maprange-1) {
				valid = true
				break
			}
		}

		if !valid {
			return fmt.Errorf("The '%s' map can't work in the current user namespace", e.ToLxcString())
		}
	}

	return nil
}

func (e *IdmapEntry) parse(s string) error {
	split := strings.Split(s, ":")
	var err error

	if len(split) != 4 {
		return fmt.Errorf("Bad idmap: %q", s)
	}

	switch split[0] {
	case "u":
		e.Isuid = true
	case "g":
		e.Isgid = true
	case "b":
		e.Isuid = true
		e.Isgid = true
	default:
		return fmt.Errorf("Bad idmap type in %q", s)
	}

	nsid, err := strconv.ParseUint(split[1], 10, 32)
	if err != nil {
		return err
	}

	e.Nsid = int64(nsid)

	hostid, err := strconv.ParseUint(split[2], 10, 32)
	if err != nil {
		return err
	}

	e.Hostid = int64(hostid)

	maprange, err := strconv.ParseUint(split[3], 10, 32)
	if err != nil {
		return err
	}

	e.Maprange = int64(maprange)

	// wraparound
	if e.Hostid+e.Maprange < e.Hostid || e.Nsid+e.Maprange < e.Nsid {
		return fmt.Errorf("Bad mapping: id wraparound")
	}

	return nil
}

/*
 * Shift a uid from the host into the container
 * I.e. 0 -> 1000 -> 101000.
 */
func (e *IdmapEntry) shift_into_ns(id int64) (int64, error) {
	if id < e.Nsid || id >= e.Nsid+e.Maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("ID mapping doesn't apply")
	}

	return id - e.Nsid + e.Hostid, nil
}

/*
 * Shift a uid from the container back to the host
 * I.e. 101000 -> 1000.
 */
func (e *IdmapEntry) shift_from_ns(id int64) (int64, error) {
	if id < e.Hostid || id >= e.Hostid+e.Maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("ID mapping doesn't apply")
	}

	return id - e.Hostid + e.Nsid, nil
}
