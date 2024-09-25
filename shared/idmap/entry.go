package idmap

import (
	"fmt"
	"strconv"
	"strings"
)

// Entry is a single idmap entry (line).
type Entry struct {
	IsUID    bool  `json:"Isuid"`
	IsGID    bool  `json:"Isgid"`
	HostID   int64 `json:"Hostid"` // id as seen on the host - i.e. 100000
	NSID     int64 `json:"Nsid"`   // id as seen in the ns - i.e. 0
	MapRange int64 `json:"Maprange"`
}

// ToLXCString converts an Entry into its LXC representation.
func (e *Entry) ToLXCString() []string {
	if e.IsUID && e.IsGID {
		return []string{
			fmt.Sprintf("u %d %d %d", e.NSID, e.HostID, e.MapRange),
			fmt.Sprintf("g %d %d %d", e.NSID, e.HostID, e.MapRange),
		}
	}

	if e.IsUID {
		return []string{fmt.Sprintf("u %d %d %d", e.NSID, e.HostID, e.MapRange)}
	}

	return []string{fmt.Sprintf("g %d %d %d", e.NSID, e.HostID, e.MapRange)}
}

// HostIDsIntersect checks whether the provided entry intersects with the host IDs of the existing one.
func (e *Entry) HostIDsIntersect(i Entry) bool {
	if (e.IsUID && i.IsUID) || (e.IsGID && i.IsGID) {
		switch {
		case isBetween(e.HostID, i.HostID, i.HostID+i.MapRange):
			return true
		case isBetween(i.HostID, e.HostID, e.HostID+e.MapRange):
			return true
		case isBetween(e.HostID+e.MapRange, i.HostID, i.HostID+i.MapRange):
			return true
		case isBetween(i.HostID+i.MapRange, e.HostID, e.HostID+e.MapRange):
			return true
		}
	}

	return false
}

// Intersects checks whether the provided entry intersects with the existing one.
func (e *Entry) Intersects(i Entry) bool {
	if (e.IsUID && i.IsUID) || (e.IsGID && i.IsGID) {
		switch {
		case isBetween(e.HostID, i.HostID, i.HostID+i.MapRange-1):
			return true
		case isBetween(i.HostID, e.HostID, e.HostID+e.MapRange-1):
			return true
		case isBetween(e.HostID+e.MapRange-1, i.HostID, i.HostID+i.MapRange-1):
			return true
		case isBetween(i.HostID+i.MapRange-1, e.HostID, e.HostID+e.MapRange-1):
			return true
		case isBetween(e.NSID, i.NSID, i.NSID+i.MapRange-1):
			return true
		case isBetween(i.NSID, e.NSID, e.NSID+e.MapRange-1):
			return true
		case isBetween(e.NSID+e.MapRange-1, i.NSID, i.NSID+i.MapRange-1):
			return true
		case isBetween(i.NSID+i.MapRange-1, e.NSID, e.NSID+e.MapRange-1):
			return true
		}
	}
	return false
}

// HostIDsCoveredBy returns whether or not the entry is covered by the supplied host UID and GID ID maps.
// If e.IsUID is true then host IDs must be covered by an entry in allowedHostUIDs, and if e.IsGID is true then
// host IDs must be covered by an entry in allowedHostGIDs.
func (e *Entry) HostIDsCoveredBy(allowedHostUIDs []Entry, allowedHostGIDs []Entry) bool {
	if !e.IsUID && !e.IsGID {
		return false // This is an invalid idmap entry.
	}

	isUIDAllowed := false

	if e.IsUID {
		for _, allowedIDMap := range allowedHostUIDs {
			if !allowedIDMap.IsUID {
				continue
			}

			if e.HostID >= allowedIDMap.HostID && (e.HostID+e.MapRange) <= (allowedIDMap.HostID+allowedIDMap.MapRange) {
				isUIDAllowed = true
				break
			}
		}
	}

	isGIDAllowed := false

	if e.IsGID {
		for _, allowedIDMap := range allowedHostGIDs {
			if !allowedIDMap.IsGID {
				continue
			}

			if e.HostID >= allowedIDMap.HostID && (e.HostID+e.MapRange) <= (allowedIDMap.HostID+allowedIDMap.MapRange) {
				isGIDAllowed = true
				break
			}
		}
	}

	return e.IsUID == isUIDAllowed && e.IsGID == isGIDAllowed
}

// Usable checks whether the entry is usable on this system.
func (e *Entry) Usable() error {
	kernelIdmap, err := NewSetFromCurrentProcess()
	if err != nil {
		return err
	}

	kernelRanges, err := kernelIdmap.ValidRanges()
	if err != nil {
		return err
	}

	// Validate the uid map.
	if e.IsUID {
		valid := false
		for _, kernelRange := range kernelRanges {
			if !kernelRange.IsUID {
				continue
			}

			if kernelRange.Contains(e.HostID) && kernelRange.Contains(e.HostID+e.MapRange-1) {
				valid = true
				break
			}
		}

		if !valid {
			return fmt.Errorf("The '%s' map can't work in the current user namespace", e.ToLXCString())
		}
	}

	// Validate the gid map.
	if e.IsGID {
		valid := false
		for _, kernelRange := range kernelRanges {
			if !kernelRange.IsGID {
				continue
			}

			if kernelRange.Contains(e.HostID) && kernelRange.Contains(e.HostID+e.MapRange-1) {
				valid = true
				break
			}
		}

		if !valid {
			return fmt.Errorf("The '%s' map can't work in the current user namespace", e.ToLXCString())
		}
	}

	return nil
}

// Clone gets a distinct copy of the entry.
func (e *Entry) Clone() *Entry {
	return &Entry{e.IsUID, e.IsGID, e.HostID, e.NSID, e.MapRange}
}

func (e *Entry) parse(s string) error {
	split := strings.Split(s, ":")
	var err error

	if len(split) != 4 {
		return fmt.Errorf("Bad idmap: %q", s)
	}

	switch split[0] {
	case "u":
		e.IsUID = true
	case "g":
		e.IsGID = true
	case "b":
		e.IsUID = true
		e.IsGID = true
	default:
		return fmt.Errorf("Bad idmap type in %q", s)
	}

	nsid, err := strconv.ParseUint(split[1], 10, 32)
	if err != nil {
		return err
	}

	e.NSID = int64(nsid)

	hostid, err := strconv.ParseUint(split[2], 10, 32)
	if err != nil {
		return err
	}

	e.HostID = int64(hostid)

	maprange, err := strconv.ParseUint(split[3], 10, 32)
	if err != nil {
		return err
	}

	e.MapRange = int64(maprange)

	// Wrap around.
	if e.HostID+e.MapRange < e.HostID || e.NSID+e.MapRange < e.NSID {
		return fmt.Errorf("Bad mapping: id wraparound")
	}

	return nil
}

// Shift a uid from the host into the container
// I.e. 0 -> 1000 -> 101000.
func (e *Entry) shiftIntoNS(id int64) (int64, error) {
	if id < e.NSID || id >= e.NSID+e.MapRange {
		// This mapping doesn't apply.
		return 0, fmt.Errorf("ID mapping doesn't apply")
	}

	return id - e.NSID + e.HostID, nil
}

// Shift a uid from the container back to the host
// I.e. 101000 -> 1000.
func (e *Entry) shiftFromNS(id int64) (int64, error) {
	if id < e.HostID || id >= e.HostID+e.MapRange {
		// This mapping doesn't apply.
		return 0, fmt.Errorf("ID mapping doesn't apply")
	}

	return id - e.HostID + e.NSID, nil
}
