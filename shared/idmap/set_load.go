package idmap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path"
	"reflect"
	"strconv"
	"strings"

	"github.com/lxc/incus/shared/util"
)

// ErrNoUserMap indicates that no entry could be found for the user.
var ErrNoUserMap = fmt.Errorf("No map found for user")

// DefaultFullKernelSet is the default Set of uid/gid with no mapping at all.
var DefaultFullKernelSet = &Set{Entries: []Entry{
	{true, false, int64(0), int64(0), int64(4294967294)},
	{false, true, int64(0), int64(0), int64(4294967294)},
}}

// NewSetFromJSON unpacks an idmap Set from its JSON representation.
func NewSetFromJSON(data string) (*Set, error) {
	ret := &Set{}
	err := json.Unmarshal([]byte(data), &ret.Entries)
	if err != nil {
		return nil, err
	}

	if len(ret.Entries) == 0 {
		return nil, nil
	}

	return ret, nil
}

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

// NewSetFromCurrentProcess returns a Set from the process' current uid/gid map.
func NewSetFromCurrentProcess() (*Set, error) {
	// Check if system doesn't have user namespaces.
	if !util.PathExists("/proc/self/uid_map") || !util.PathExists("/proc/self/gid_map") {
		// Without a user namespace, just return the full map.
		return DefaultFullKernelSet, nil
	}

	ret := &Set{}

	// Parse the uidmap.
	entries, err := getFromProc("/proc/self/uid_map")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		e := Entry{IsUID: true, NSID: entry[0], HostID: entry[1], MapRange: entry[2]}
		ret.Entries = append(ret.Entries, e)
	}

	// Parse the gidmap.
	entries, err = getFromProc("/proc/self/gid_map")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		e := Entry{IsGID: true, NSID: entry[0], HostID: entry[1], MapRange: entry[2]}
		ret.Entries = append(ret.Entries, e)
	}

	return ret, nil
}

// DefaultSet returns the system's idmapset.
func DefaultSet(rootfs string, username string) (*Set, error) {
	idmapset := new(Set)

	if username == "" {
		currentUser, err := user.Current()
		if err != nil {
			return nil, err
		}

		username = currentUser.Username
	}

	// Check if shadow's uidmap tools are installed.
	subuidPath := path.Join(rootfs, "/etc/subuid")
	subgidPath := path.Join(rootfs, "/etc/subgid")
	if util.PathExists(subuidPath) && util.PathExists(subgidPath) {
		// Parse the shadow uidmap.
		entries, err := getFromShadow(subuidPath, username)
		if err != nil {
			if username == "root" && err == ErrNoUserMap {
				// No root map available, figure out a default map.
				return kernelDefaultMap()
			}

			return nil, err
		}

		for _, entry := range entries {
			e := Entry{IsUID: true, NSID: 0, HostID: entry[0], MapRange: entry[1]}
			idmapset.Entries = append(idmapset.Entries, e)
		}

		// Parse the shadow gidmap.
		entries, err = getFromShadow(subgidPath, username)
		if err != nil {
			if username == "root" && err == ErrNoUserMap {
				// No root map available, figure out a default map.
				return kernelDefaultMap()
			}

			return nil, err
		}

		for _, entry := range entries {
			e := Entry{IsGID: true, NSID: 0, HostID: entry[0], MapRange: entry[1]}
			idmapset.Entries = append(idmapset.Entries, e)
		}

		return idmapset, nil
	}

	// No shadow available, figure out a default map.
	return kernelDefaultMap()
}

func getFromShadow(fname string, username string) ([][]int64, error) {
	entries := [][]int64{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments.
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format.
		s = strings.Split(s[0], ":")
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		if strings.EqualFold(s[0], username) {
			// Get range start.
			entryStart, err := strconv.ParseUint(s[1], 10, 32)
			if err != nil {
				continue
			}

			// Get range size.
			entrySize, err := strconv.ParseUint(s[2], 10, 32)
			if err != nil {
				continue
			}

			entries = append(entries, []int64{int64(entryStart), int64(entrySize)})
		}
	}

	if len(entries) == 0 {
		return nil, ErrNoUserMap
	}

	return entries, nil
}

func getFromProc(fname string) ([][]int64, error) {
	entries := [][]int64{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments.
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format.
		s = strings.Fields(s[0])
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		// Get range start.
		entryStart, err := strconv.ParseUint(s[0], 10, 32)
		if err != nil {
			continue
		}

		// Get range size.
		entryHost, err := strconv.ParseUint(s[1], 10, 32)
		if err != nil {
			continue
		}

		// Get range size.
		entrySize, err := strconv.ParseUint(s[2], 10, 32)
		if err != nil {
			continue
		}

		entries = append(entries, []int64{int64(entryStart), int64(entryHost), int64(entrySize)})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("Namespace doesn't have any map set")
	}

	return entries, nil
}

func kernelDefaultMap() (*Set, error) {
	idmapset := new(Set)

	kernelMap, err := NewSetFromCurrentProcess()
	if err != nil {
		// Hardcoded fallback map.
		e := Entry{IsUID: true, IsGID: false, NSID: 0, HostID: 1000000, MapRange: 1000000000}
		idmapset.Entries = append(idmapset.Entries, e)

		e = Entry{IsUID: false, IsGID: true, NSID: 0, HostID: 1000000, MapRange: 1000000000}
		idmapset.Entries = append(idmapset.Entries, e)
		return idmapset, nil
	}

	// Look for mapped ranges.
	kernelRanges, err := kernelMap.ValidRanges()
	if err != nil {
		return nil, err
	}

	// Special case for when we have the full kernel range.
	fullKernelRanges := []*Range{
		{true, false, int64(0), int64(4294967294)},
		{false, true, int64(0), int64(4294967294)}}

	if reflect.DeepEqual(kernelRanges, fullKernelRanges) {
		// Hardcoded fallback map.
		e := Entry{IsUID: true, IsGID: false, NSID: 0, HostID: 1000000, MapRange: 1000000000}
		idmapset.Entries = append(idmapset.Entries, e)

		e = Entry{IsUID: false, IsGID: true, NSID: 0, HostID: 1000000, MapRange: 1000000000}
		idmapset.Entries = append(idmapset.Entries, e)
		return idmapset, nil
	}

	// Find a suitable uid range.
	for _, entry := range kernelRanges {
		// We only care about uids right now.
		if !entry.IsUID {
			continue
		}

		// We want a map that's separate from the system's own POSIX allocation.
		if entry.EndID < 100000 {
			continue
		}

		// Don't use the first 100000 ids.
		if entry.StartID < 100000 {
			entry.StartID = 100000
		}

		// Check if we have enough ids.
		if entry.EndID-entry.StartID < 65536 {
			continue
		}

		// Add the map.
		e := Entry{IsUID: true, IsGID: false, NSID: 0, HostID: entry.StartID, MapRange: entry.EndID - entry.StartID + 1}
		idmapset.Entries = append(idmapset.Entries, e)
	}

	// Find a suitable gid range.
	for _, entry := range kernelRanges {
		// We only care about gids right now.
		if !entry.IsGID {
			continue
		}

		// We want a map that's separate from the system's own POSIX allocation.
		if entry.EndID < 100000 {
			continue
		}

		// Don't use the first 100000 ids.
		if entry.StartID < 100000 {
			entry.StartID = 100000
		}

		// Check if we have enough ids.
		if entry.EndID-entry.StartID < 65536 {
			continue
		}

		// Add the map.
		e := Entry{IsUID: false, IsGID: true, NSID: 0, HostID: entry.StartID, MapRange: entry.EndID - entry.StartID + 1}
		idmapset.Entries = append(idmapset.Entries, e)
	}

	return idmapset, nil
}
