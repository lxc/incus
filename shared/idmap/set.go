package idmap

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/lxc/incus/v6/shared/util"
)

// ErrHostIDIsSubID indicates that an expected host ID is part of a subid range.
var ErrHostIDIsSubID = fmt.Errorf("Host ID is in the range of subids")

// ErrNoSuitableSubmap indicates that it was impossible to split a submap with the requested characteristics.
var ErrNoSuitableSubmap = fmt.Errorf("Couldn't find a suitable submap")

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
		// Check if the existing entry intersects with the new one.
		if !e.Intersects(i) {
			result = append(result, e)
			continue
		}

		// Fail when the same hostid(s) are used in multiple entries.
		if e.HostIDsIntersect(i) {
			return ErrHostIDIsSubID
		}

		// Split the lower part of the entry (ids from beginning of existing entry to start of new entry).
		lower := Entry{
			IsUID:    e.IsUID,
			IsGID:    e.IsGID,
			HostID:   e.HostID,
			NSID:     e.NSID,
			MapRange: i.NSID - e.NSID,
		}

		// Split the upper part of the entry (ids from new entry to end of existing entry).
		upper := Entry{
			IsUID:    e.IsUID,
			IsGID:    e.IsGID,
			HostID:   e.HostID + lower.MapRange + i.MapRange,
			NSID:     i.NSID + i.MapRange,
			MapRange: e.MapRange - i.MapRange - lower.MapRange,
		}

		// If the new entry doesn't completely cover the lower part of
		// the existing entry, then add that to the set.
		if lower.MapRange > 0 {
			result = append(result, lower)
		}

		// Add the new entry in the middle.
		if !added {
			// With an entry matching both uid and gid, more than one
			// intersection is possible, keep track of it to only put it in the set once.
			added = true
			result = append(result, i)
		}

		// If the new entry doesn't completely cover the upper part of
		// the existing entry, then add that to the set.
		if upper.MapRange > 0 {
			result = append(result, upper)
		}
	}

	// If no intersection was found, just add the new entry to the set.
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
			if !slices.Contains(lines, l) {
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

func (m *Set) doShiftIntoNS(uid int64, gid int64, how string) (int64, int64) {
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

// ShiftIntoNS shifts the provided uid and gid into their container equivalent.
func (m *Set) ShiftIntoNS(uid int64, gid int64) (int64, int64) {
	return m.doShiftIntoNS(uid, gid, "in")
}

// ShiftFromNS shifts the provided uid and gid into their host equivalent.
func (m *Set) ShiftFromNS(uid int64, gid int64) (int64, int64) {
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

// Split returns a new Set made from a subset of the original set.
// The minimum and maximum number of uid/gid included is configurable as is the minimum and maximum host ID.
func (m *Set) Split(minSize int64, maxSize int64, minHost int64, maxHost int64) (*Set, error) {
	var uidEntry *Entry
	var gidEntry *Entry

	for _, entry := range m.Entries {
		// Make a local copy we can modify.
		newEntry := entry.Clone()

		// Check if too small.
		if minSize != -1 && newEntry.MapRange < minSize {
			continue
		}

		// Check if host id is too high.
		if maxHost != -1 && newEntry.HostID > maxHost {
			continue
		}

		// Check if host id is too low.
		if minHost != -1 && newEntry.HostID < minHost {
			// Check if we can just shift the beginning to match.
			delta := minHost - newEntry.HostID
			if newEntry.MapRange-delta < minSize {
				continue
			}

			// Update the beginning size of the range to match.
			newEntry.HostID = minHost
			newEntry.MapRange -= delta
		}

		// Cap to maxSize if set.
		if maxSize != -1 && newEntry.MapRange > maxSize {
			newEntry.MapRange = maxSize
		}

		// Pick the range if it's larger than what we currently have.
		if newEntry.IsUID && (uidEntry == nil || uidEntry.MapRange < newEntry.MapRange) {
			newEntry.IsGID = false
			uidEntry = newEntry
		}

		if newEntry.IsGID && (gidEntry == nil || gidEntry.MapRange < newEntry.MapRange) {
			newEntry.IsUID = false
			gidEntry = newEntry
		}
	}

	if uidEntry == nil || gidEntry == nil {
		return nil, ErrNoSuitableSubmap
	}

	return &Set{Entries: []Entry{*uidEntry, *gidEntry}}, nil
}

// Includes checks whether the provided Set is fully covered by the current Set.
func (m *Set) Includes(sub *Set) bool {
	// Populate the allowed entries.
	allowedUIDs := []Entry{}
	allowedGIDs := []Entry{}

	for _, entry := range m.Entries {
		if entry.IsUID {
			allowedUIDs = append(allowedUIDs, entry)
		}

		if entry.IsGID {
			allowedGIDs = append(allowedGIDs, entry)
		}
	}

	// Check for coverage.
	for _, entry := range sub.Entries {
		if !entry.HostIDsCoveredBy(allowedUIDs, allowedGIDs) {
			return false
		}
	}

	return true
}
