package idmap

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/lxc/incus/shared/util"
)

// ErrHostIDIsSubID indicates that an expected host ID is part of a subid range.
var ErrHostIDIsSubID = fmt.Errorf("Host ID is in the range of subids")

// Set is a list of Entry with some functions on it.
type Set struct {
	Entries []Entry
}

// Equals checks if two Set are functionally identical.
func (m *Set) Equals(other *Set) bool {
	// Get comparable maps.
	expandSortIdmap := func(input *Set) *Set {
		if input == nil {
			input = &Set{}
		}

		newEntries := []Entry{}

		for _, entry := range input.Entries {
			if entry.IsUID && entry.IsGID {
				newEntries = append(newEntries, Entry{true, false, entry.HostID, entry.NSID, entry.MapRange})
				newEntries = append(newEntries, Entry{false, true, entry.HostID, entry.NSID, entry.MapRange})
			} else {
				newEntries = append(newEntries, entry)
			}
		}

		output := &Set{Entries: newEntries}
		sort.Sort(output)

		return output
	}

	// Actually compare.
	return reflect.DeepEqual(expandSortIdmap(m), expandSortIdmap(other))
}

// Len returns the number of Entry contained in the set.
func (m *Set) Len() int {
	return len(m.Entries)
}

// Swap allows swapping two Entry in the set (used for sorting).
func (m *Set) Swap(i, j int) {
	m.Entries[i], m.Entries[j] = m.Entries[j], m.Entries[i]
}

// Less compares two Entry in the set (used for sorting).
func (m *Set) Less(i, j int) bool {
	if m.Entries[i].IsUID != m.Entries[j].IsUID {
		return m.Entries[i].IsUID
	}

	if m.Entries[i].IsGID != m.Entries[j].IsGID {
		return m.Entries[i].IsGID
	}

	return m.Entries[i].NSID < m.Entries[j].NSID
}

// Intersects checks if any of the Entry in the set intersects with the provided entry.
func (m *Set) Intersects(i Entry) bool {
	for _, e := range m.Entries {
		if i.Intersects(e) {
			return true
		}
	}

	return false
}

// HostIDsIntersect checks if any of the Entry hostids in the set intersects with the provided entry.
func (m *Set) HostIDsIntersect(i Entry) bool {
	for _, e := range m.Entries {
		if i.HostIDsIntersect(e) {
			return true
		}
	}

	return false
}

// Usable checks that all Entry in the set are usable.
func (m *Set) Usable() error {
	for _, e := range m.Entries {
		err := e.Usable()
		if err != nil {
			return err
		}
	}

	return nil
}

// FilterPOSIX returns a copy of the set with only entries that have a minimum of 65536 IDs.
func (m *Set) FilterPOSIX() *Set {
	filtered := &Set{Entries: []Entry{}}

	for _, entry := range m.Entries {
		if entry.MapRange < 65536 {
			continue
		}

		filtered.Entries = append(filtered.Entries, entry)
	}

	if len(filtered.Entries) == 0 {
		return nil
	}

	return filtered
}

// ValidRanges turns the set into a slice of Range.
func (m *Set) ValidRanges() ([]*Range, error) {
	ranges := []*Range{}

	// Sort the map.
	idmap := &Set{}
	err := util.DeepCopy(&m, &idmap)
	if err != nil {
		return nil, err
	}

	sort.Sort(idmap)

	for _, mapEntry := range idmap.Entries {
		var entry *Range

		for _, idEntry := range ranges {
			if mapEntry.IsUID != idEntry.IsUID || mapEntry.IsGID != idEntry.IsGID {
				continue
			}

			if idEntry.EndID+1 == mapEntry.NSID {
				entry = idEntry
				break
			}
		}

		if entry != nil {
			entry.EndID = entry.EndID + mapEntry.MapRange
			continue
		}

		ranges = append(ranges, &Range{
			IsUID:   mapEntry.IsUID,
			IsGID:   mapEntry.IsGID,
			StartID: mapEntry.NSID,
			EndID:   mapEntry.NSID + mapEntry.MapRange - 1,
		})
	}

	return ranges, nil
}

// AddSafe adds an entry to the idmap set, breaking apart any ranges that the
// new idmap intersects with in the process.
func (m *Set) AddSafe(i Entry) error {
	result := []Entry{}
	added := false
	for _, e := range m.Entries {
		if !e.Intersects(i) {
			result = append(result, e)
			continue
		}

		if e.HostIDsIntersect(i) {
			return ErrHostIDIsSubID
		}

		added = true

		lower := Entry{
			IsUID:    e.IsUID,
			IsGID:    e.IsGID,
			HostID:   e.HostID,
			NSID:     e.NSID,
			MapRange: i.NSID - e.NSID,
		}

		upper := Entry{
			IsUID:    e.IsUID,
			IsGID:    e.IsGID,
			HostID:   e.HostID + lower.MapRange + i.MapRange,
			NSID:     i.NSID + i.MapRange,
			MapRange: e.MapRange - i.MapRange - lower.MapRange,
		}

		if lower.MapRange > 0 {
			result = append(result, lower)
		}

		result = append(result, i)
		if upper.MapRange > 0 {
			result = append(result, upper)
		}
	}

	if !added {
		result = append(result, i)
	}

	m.Entries = result

	return nil
}

// ToLXCString converts the set to a slice of LXC configuration entries.
func (m *Set) ToLXCString() []string {
	var lines []string
	for _, e := range m.Entries {
		for _, l := range e.ToLXCString() {
			if !util.ValueInSlice(l, lines) {
				lines = append(lines, l)
			}
		}
	}

	return lines
}

// Append adds an entry to the set.
func (m *Set) Append(s string) (*Set, error) {
	e := Entry{}

	err := e.parse(s)
	if err != nil {
		return m, err
	}

	if m.Intersects(e) {
		return m, fmt.Errorf("Conflicting id mapping")
	}

	m.Entries = append(m.Entries, e)
	return m, nil
}

func (m Set) doShiftIntoNS(uid int64, gid int64, how string) (int64, int64) {
	u := int64(-1)
	g := int64(-1)

	for _, e := range m.Entries {
		var err error
		var tmpu, tmpg int64
		if e.IsUID && u == -1 {
			switch how {
			case "in":
				tmpu, err = e.shiftIntoNS(uid)
			case "out":
				tmpu, err = e.shiftFromNS(uid)
			}

			if err == nil {
				u = tmpu
			}
		}

		if e.IsGID && g == -1 {
			switch how {
			case "in":
				tmpg, err = e.shiftIntoNS(gid)
			case "out":
				tmpg, err = e.shiftFromNS(gid)
			}

			if err == nil {
				g = tmpg
			}
		}
	}

	return u, g
}

// ShiftIntoNS shiftfs the provided uid and gid into their container equivalent.
func (m Set) ShiftIntoNS(uid int64, gid int64) (int64, int64) {
	return m.doShiftIntoNS(uid, gid, "in")
}

// ShiftFromNS shiftfs the provided uid and gid into their host equivalent.
func (m Set) ShiftFromNS(uid int64, gid int64) (int64, int64) {
	return m.doShiftIntoNS(uid, gid, "out")
}

// ToJSON marshals a Set to its JSON reprensetation.
func (m *Set) ToJSON() (string, error) {
	if m == nil {
		return "[]", nil
	}

	out, err := json.Marshal(m.Entries)
	if err != nil {
		return "", err
	}

	return string(out), nil
}

// ByHostID allows for sorting an Set by host id.
type ByHostID Set

func (s ByHostID) Len() int {
	return len(s.Entries)
}

func (s ByHostID) Swap(i, j int) {
	s.Entries[i], s.Entries[j] = s.Entries[j], s.Entries[i]
}

func (s ByHostID) Less(i, j int) bool {
	return s.Entries[i].HostID < s.Entries[j].HostID
}
