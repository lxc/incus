package idmap

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetAddSafe_split(t *testing.T) {
	orig := Set{Entries: []Entry{{IsUID: true, HostID: 1000, NSID: 0, MapRange: 1000}}}

	err := orig.AddSafe(Entry{IsUID: true, HostID: 500, NSID: 500, MapRange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Entries[0].HostID != 1000 || orig.Entries[0].NSID != 0 || orig.Entries[0].MapRange != 500 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[0]))
		return
	}

	if orig.Entries[1].HostID != 500 || orig.Entries[1].NSID != 500 || orig.Entries[1].MapRange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[1]))
		return
	}

	if orig.Entries[2].HostID != 1510 || orig.Entries[2].NSID != 510 || orig.Entries[2].MapRange != 490 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[2]))
		return
	}

	if len(orig.Entries) != 3 {
		t.Error("too many idmap entries")
		return
	}
}

func TestSetAddSafe_lower(t *testing.T) {
	orig := Set{Entries: []Entry{{IsUID: true, HostID: 1000, NSID: 0, MapRange: 1000}}}

	err := orig.AddSafe(Entry{IsUID: true, HostID: 500, NSID: 0, MapRange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Entries[0].HostID != 500 || orig.Entries[0].NSID != 0 || orig.Entries[0].MapRange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[0]))
		return
	}

	if orig.Entries[1].HostID != 1010 || orig.Entries[1].NSID != 10 || orig.Entries[1].MapRange != 990 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[1]))
		return
	}

	if len(orig.Entries) != 2 {
		t.Error("too many idmap entries")
		return
	}
}

func TestSetAddSafe_upper(t *testing.T) {
	orig := Set{Entries: []Entry{{IsUID: true, HostID: 1000, NSID: 0, MapRange: 1000}}}

	err := orig.AddSafe(Entry{IsUID: true, HostID: 500, NSID: 995, MapRange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Entries[0].HostID != 1000 || orig.Entries[0].NSID != 0 || orig.Entries[0].MapRange != 995 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[0]))
		return
	}

	if orig.Entries[1].HostID != 500 || orig.Entries[1].NSID != 995 || orig.Entries[1].MapRange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Entries[1]))
		return
	}

	if len(orig.Entries) != 2 {
		t.Error("too many idmap entries")
		return
	}
}

func TestSetIntersects(t *testing.T) {
	orig := Set{Entries: []Entry{{IsUID: true, HostID: 165536, NSID: 0, MapRange: 65536}}}

	if !orig.Intersects(Entry{IsUID: true, HostID: 231071, NSID: 0, MapRange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if !orig.Intersects(Entry{IsUID: true, HostID: 231072, NSID: 0, MapRange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if !orig.Intersects(Entry{IsUID: true, HostID: 231072, NSID: 65535, MapRange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if orig.Intersects(Entry{IsUID: true, HostID: 231072, NSID: 65536, MapRange: 65536}) {
		t.Error("ranges intersect")
		return
	}
}

func TestIdmapHostIDMapRange(t *testing.T) {
	// Check empty entry is not covered.
	idmap := Entry{}
	assert.Equal(t, false, idmap.HostIDsCoveredBy(nil, nil))

	// Check nil allowed lists are not covered.
	idmap = Entry{IsUID: true, HostID: 1000, MapRange: 1}
	assert.Equal(t, false, idmap.HostIDsCoveredBy(nil, nil))

	// Check that UID/GID specific host IDs are covered by equivalent UID/GID specific host ID rule.
	uidOnlyEntry := Entry{IsUID: true, HostID: 1000, MapRange: 1}
	gidOnlyEntry := Entry{IsGID: true, HostID: 1000, MapRange: 1}

	allowedUIDMaps := []Entry{
		{IsUID: true, HostID: 1000, MapRange: 1},
	}

	allowedGIDMaps := []Entry{
		{IsGID: true, HostID: 1000, MapRange: 1},
	}

	assert.Equal(t, true, uidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.Equal(t, false, uidOnlyEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.Equal(t, true, uidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.Equal(t, false, uidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.Equal(t, false, uidOnlyEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.Equal(t, false, uidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	assert.Equal(t, false, gidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.Equal(t, true, gidOnlyEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.Equal(t, true, gidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	assert.Equal(t, false, gidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.Equal(t, false, gidOnlyEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.Equal(t, false, gidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	// Check ranges are correctly blocked when not covered by single ID allow list.
	uidOnlyRangeEntry := Entry{IsUID: true, HostID: 1000, MapRange: 2}
	gidOnlyRangeEntry := Entry{IsGID: true, HostID: 1000, MapRange: 2}

	assert.Equal(t, false, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.Equal(t, false, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.Equal(t, false, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.Equal(t, false, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.Equal(t, false, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.Equal(t, false, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	// Check ranges are allowed when fully covered.
	allowedUIDMaps = []Entry{
		{IsUID: true, HostID: 1000, MapRange: 2},
	}

	allowedGIDMaps = []Entry{
		{IsGID: true, HostID: 1000, MapRange: 2},
	}

	assert.Equal(t, true, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.Equal(t, false, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.Equal(t, true, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.Equal(t, false, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.Equal(t, true, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.Equal(t, true, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	// Check ranges for combined allowed ID maps are correctly validated.
	allowedCombinedMaps := []Entry{
		{IsUID: true, IsGID: true, HostID: 1000, MapRange: 2},
	}

	assert.Equal(t, true, uidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.Equal(t, false, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.Equal(t, true, uidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	assert.Equal(t, false, gidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.Equal(t, true, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.Equal(t, true, gidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	combinedEntry := Entry{IsUID: true, IsGID: true, HostID: 1000, MapRange: 1}

	assert.Equal(t, false, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.Equal(t, false, combinedEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.Equal(t, true, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	assert.Equal(t, false, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.Equal(t, false, combinedEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.Equal(t, true, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))
}
