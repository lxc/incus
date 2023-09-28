//go:build linux && cgo

package idmap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

const VFS3FscapsUnsupported int32 = 0
const VFS3FscapsSupported int32 = 1
const VFS3FscapsUnknown int32 = -1

var VFS3Fscaps int32 = VFS3FscapsUnknown
var ErrNoUserMap = fmt.Errorf("No map found for user")

type IdRange struct {
	Isuid   bool
	Isgid   bool
	Startid int64
	Endid   int64
}

func (i *IdRange) Contains(id int64) bool {
	return id >= i.Startid && id <= i.Endid
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

func is_between(x, low, high int64) bool {
	return x >= low && x < high
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

type ByHostid []*IdmapEntry

func (s ByHostid) Len() int {
	return len(s)
}

func (s ByHostid) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByHostid) Less(i, j int) bool {
	return s[i].Hostid < s[j].Hostid
}

/* taken from http://blog.golang.org/slices (which is under BSD licence). */
func Extend(slice []IdmapEntry, element IdmapEntry) []IdmapEntry {
	n := len(slice)
	if n == cap(slice) {
		// Slice is full; must grow.
		// We double its size and add 1, so if the size is zero we still grow.
		newSlice := make([]IdmapEntry, len(slice), 2*len(slice)+1)
		copy(newSlice, slice)
		slice = newSlice
	}

	slice = slice[0 : n+1]
	slice[n] = element
	return slice
}

func (m *IdmapSet) Equals(other *IdmapSet) bool {
	// Get comparable maps
	expandSortIdmap := func(input *IdmapSet) IdmapSet {
		if input == nil {
			input = &IdmapSet{}
		}

		newEntries := []IdmapEntry{}

		for _, entry := range input.Idmap {
			if entry.Isuid && entry.Isgid {
				newEntries = append(newEntries, IdmapEntry{true, false, entry.Hostid, entry.Nsid, entry.Maprange})
				newEntries = append(newEntries, IdmapEntry{false, true, entry.Hostid, entry.Nsid, entry.Maprange})
			} else {
				newEntries = append(newEntries, entry)
			}
		}

		output := IdmapSet{Idmap: newEntries}
		sort.Sort(output)

		return output
	}

	// Actually compare
	return reflect.DeepEqual(expandSortIdmap(m), expandSortIdmap(other))
}

func (m IdmapSet) Len() int {
	return len(m.Idmap)
}

func (m IdmapSet) Swap(i, j int) {
	m.Idmap[i], m.Idmap[j] = m.Idmap[j], m.Idmap[i]
}

func (m IdmapSet) Less(i, j int) bool {
	if m.Idmap[i].Isuid != m.Idmap[j].Isuid {
		return m.Idmap[i].Isuid
	}

	if m.Idmap[i].Isgid != m.Idmap[j].Isgid {
		return m.Idmap[i].Isgid
	}

	return m.Idmap[i].Nsid < m.Idmap[j].Nsid
}

func (m IdmapSet) Intersects(i IdmapEntry) bool {
	for _, e := range m.Idmap {
		if i.Intersects(e) {
			return true
		}
	}
	return false
}

func (m IdmapSet) HostidsIntersect(i IdmapEntry) bool {
	for _, e := range m.Idmap {
		if i.HostidsIntersect(e) {
			return true
		}
	}
	return false
}

func (m IdmapSet) Usable() error {
	for _, e := range m.Idmap {
		err := e.Usable()
		if err != nil {
			return err
		}
	}

	return nil
}

func (m IdmapSet) ValidRanges() ([]*IdRange, error) {
	ranges := []*IdRange{}

	// Sort the map
	idmap := IdmapSet{}
	err := util.DeepCopy(&m, &idmap)
	if err != nil {
		return nil, err
	}

	sort.Sort(idmap)

	for _, mapEntry := range idmap.Idmap {
		var entry *IdRange
		for _, idEntry := range ranges {
			if mapEntry.Isuid != idEntry.Isuid || mapEntry.Isgid != idEntry.Isgid {
				continue
			}

			if idEntry.Endid+1 == mapEntry.Nsid {
				entry = idEntry
				break
			}
		}

		if entry != nil {
			entry.Endid = entry.Endid + mapEntry.Maprange
			continue
		}

		ranges = append(ranges, &IdRange{
			Isuid:   mapEntry.Isuid,
			Isgid:   mapEntry.Isgid,
			Startid: mapEntry.Nsid,
			Endid:   mapEntry.Nsid + mapEntry.Maprange - 1,
		})
	}

	return ranges, nil
}

var ErrHostIdIsSubId = fmt.Errorf("Host id is in the range of subids")

/* AddSafe adds an entry to the idmap set, breaking apart any ranges that the
 * new idmap intersects with in the process.
 */
func (m *IdmapSet) AddSafe(i IdmapEntry) error {
	result := []IdmapEntry{}
	added := false
	for _, e := range m.Idmap {
		if !e.Intersects(i) {
			result = append(result, e)
			continue
		}

		if e.HostidsIntersect(i) {
			return ErrHostIdIsSubId
		}

		added = true

		lower := IdmapEntry{
			Isuid:    e.Isuid,
			Isgid:    e.Isgid,
			Hostid:   e.Hostid,
			Nsid:     e.Nsid,
			Maprange: i.Nsid - e.Nsid,
		}

		upper := IdmapEntry{
			Isuid:    e.Isuid,
			Isgid:    e.Isgid,
			Hostid:   e.Hostid + lower.Maprange + i.Maprange,
			Nsid:     i.Nsid + i.Maprange,
			Maprange: e.Maprange - i.Maprange - lower.Maprange,
		}

		if lower.Maprange > 0 {
			result = append(result, lower)
		}

		result = append(result, i)
		if upper.Maprange > 0 {
			result = append(result, upper)
		}
	}

	if !added {
		result = append(result, i)
	}

	m.Idmap = result
	return nil
}

func (m IdmapSet) ToLxcString() []string {
	var lines []string
	for _, e := range m.Idmap {
		for _, l := range e.ToLxcString() {
			if !util.ValueInSlice(l, lines) {
				lines = append(lines, l)
			}
		}
	}

	return lines
}

func (m IdmapSet) ToUidMappings() []syscall.SysProcIDMap {
	mapping := []syscall.SysProcIDMap{}

	for _, e := range m.Idmap {
		if !e.Isuid {
			continue
		}

		mapping = append(mapping, syscall.SysProcIDMap{
			ContainerID: int(e.Nsid),
			HostID:      int(e.Hostid),
			Size:        int(e.Maprange),
		})
	}

	return mapping
}

func (m IdmapSet) ToGidMappings() []syscall.SysProcIDMap {
	mapping := []syscall.SysProcIDMap{}

	for _, e := range m.Idmap {
		if !e.Isgid {
			continue
		}

		mapping = append(mapping, syscall.SysProcIDMap{
			ContainerID: int(e.Nsid),
			HostID:      int(e.Hostid),
			Size:        int(e.Maprange),
		})
	}

	return mapping
}

func (m IdmapSet) Append(s string) (IdmapSet, error) {
	e := IdmapEntry{}
	err := e.parse(s)
	if err != nil {
		return m, err
	}

	if m.Intersects(e) {
		return m, fmt.Errorf("Conflicting id mapping")
	}

	m.Idmap = Extend(m.Idmap, e)
	return m, nil
}

func (m IdmapSet) doShiftIntoNs(uid int64, gid int64, how string) (int64, int64) {
	u := int64(-1)
	g := int64(-1)

	for _, e := range m.Idmap {
		var err error
		var tmpu, tmpg int64
		if e.Isuid && u == -1 {
			switch how {
			case "in":
				tmpu, err = e.shift_into_ns(uid)
			case "out":
				tmpu, err = e.shift_from_ns(uid)
			}

			if err == nil {
				u = tmpu
			}
		}

		if e.Isgid && g == -1 {
			switch how {
			case "in":
				tmpg, err = e.shift_into_ns(gid)
			case "out":
				tmpg, err = e.shift_from_ns(gid)
			}

			if err == nil {
				g = tmpg
			}
		}
	}

	return u, g
}

func (m IdmapSet) ShiftIntoNs(uid int64, gid int64) (int64, int64) {
	return m.doShiftIntoNs(uid, gid, "in")
}

func (m IdmapSet) ShiftFromNs(uid int64, gid int64) (int64, int64) {
	return m.doShiftIntoNs(uid, gid, "out")
}

func (set *IdmapSet) doUidshiftIntoContainer(dir string, testmode bool, how string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	if how == "in" && atomic.LoadInt32(&VFS3Fscaps) == VFS3FscapsUnknown {
		if SupportsVFS3Fscaps(dir) {
			atomic.StoreInt32(&VFS3Fscaps, VFS3FscapsSupported)
		} else {
			atomic.StoreInt32(&VFS3Fscaps, VFS3FscapsUnsupported)
		}
	}

	// Expand any symlink before the final path component
	tmp := filepath.Dir(dir)
	tmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return fmt.Errorf("Failed expanding symlinks of %q: %w", tmp, err)
	}

	dir = filepath.Join(tmp, filepath.Base(dir))
	dir = strings.TrimRight(dir, "/")

	hardLinks := []uint64{}
	convert := func(path string, fi os.FileInfo, err error) (e error) {
		if err != nil {
			return err
		}

		if skipper != nil && skipper(dir, path, fi) {
			return filepath.SkipDir
		}

		var stat unix.Stat_t
		err = unix.Lstat(path, &stat)
		if err != nil {
			return err
		}

		if stat.Nlink >= 2 {
			for _, linkInode := range hardLinks {
				// File was already shifted through hardlink
				if linkInode == stat.Ino {
					return nil
				}
			}

			hardLinks = append(hardLinks, stat.Ino)
		}

		uid := int64(stat.Uid)
		gid := int64(stat.Gid)
		caps := []byte{}

		var newuid, newgid int64
		switch how {
		case "in":
			newuid, newgid = set.ShiftIntoNs(uid, gid)
		case "out":
			newuid, newgid = set.ShiftFromNs(uid, gid)
		}

		if testmode {
			fmt.Printf("I would shift %q to %d %d\n", path, newuid, newgid)
		} else {
			// Dump capabilities
			if fi.Mode()&os.ModeSymlink == 0 {
				caps, err = GetCaps(path)
				if err != nil {
					return err
				}
			}

			// Shift owner
			err = ShiftOwner(dir, path, int(newuid), int(newgid))
			if err != nil {
				return err
			}

			if fi.Mode()&os.ModeSymlink == 0 {
				// Shift POSIX ACLs
				err = ShiftACL(path, func(uid int64, gid int64) (int64, int64) { return set.doShiftIntoNs(uid, gid, how) })
				if err != nil {
					return err
				}

				// Shift capabilities
				if len(caps) != 0 {
					rootUID := int64(0)
					if how == "in" {
						rootUID, _ = set.ShiftIntoNs(0, 0)
					}

					if how != "in" || atomic.LoadInt32(&VFS3Fscaps) == VFS3FscapsSupported {
						err = SetCaps(path, caps, rootUID)
						if err != nil {
							logger.Warnf("Unable to set file capabilities on %q: %v", path, err)
						}
					}
				}
			}
		}

		return nil
	}

	if !util.PathExists(dir) {
		return fmt.Errorf("No such file or directory: %q", dir)
	}

	return filepath.Walk(dir, convert)
}

func (set *IdmapSet) UidshiftIntoContainer(dir string, testmode bool) error {
	return set.doUidshiftIntoContainer(dir, testmode, "in", nil)
}

func (set *IdmapSet) UidshiftFromContainer(dir string, testmode bool) error {
	return set.doUidshiftIntoContainer(dir, testmode, "out", nil)
}

func (set *IdmapSet) ShiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return set.doUidshiftIntoContainer(p, false, "in", skipper)
}

func (set *IdmapSet) UnshiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return set.doUidshiftIntoContainer(p, false, "out", skipper)
}

func (set *IdmapSet) ShiftFile(p string) error {
	return set.ShiftRootfs(p, nil)
}

/*
 * get a uid or gid mapping from /etc/subxid.
 */
func getFromShadow(fname string, username string) ([][]int64, error) {
	entries := [][]int64{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format
		s = strings.Split(s[0], ":")
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		if strings.EqualFold(s[0], username) {
			// Get range start
			entryStart, err := strconv.ParseUint(s[1], 10, 32)
			if err != nil {
				continue
			}

			// Get range size
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

/*
 * get a uid or gid mapping from /proc/self/{g,u}id_map.
 */
func getFromProc(fname string) ([][]int64, error) {
	entries := [][]int64{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format
		s = strings.Fields(s[0])
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		// Get range start
		entryStart, err := strconv.ParseUint(s[0], 10, 32)
		if err != nil {
			continue
		}

		// Get range size
		entryHost, err := strconv.ParseUint(s[1], 10, 32)
		if err != nil {
			continue
		}

		// Get range size
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

/*
 * Create a new default idmap.
 */
func DefaultIdmapSet(rootfs string, username string) (*IdmapSet, error) {
	idmapset := new(IdmapSet)

	if username == "" {
		currentUser, err := user.Current()
		if err != nil {
			return nil, err
		}

		username = currentUser.Username
	}

	// Check if shadow's uidmap tools are installed
	subuidPath := path.Join(rootfs, "/etc/subuid")
	subgidPath := path.Join(rootfs, "/etc/subgid")
	if util.PathExists(subuidPath) && util.PathExists(subgidPath) {
		// Parse the shadow uidmap
		entries, err := getFromShadow(subuidPath, username)
		if err != nil {
			if username == "root" && err == ErrNoUserMap {
				// No root map available, figure out a default map
				return kernelDefaultMap()
			}

			return nil, err
		}

		for _, entry := range entries {
			// Check that it's big enough to be useful
			if int(entry[1]) < 65536 {
				continue
			}

			e := IdmapEntry{Isuid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)

			// NOTE: Remove once we can deal with multiple shadow maps
			break
		}

		// Parse the shadow gidmap
		entries, err = getFromShadow(subgidPath, username)
		if err != nil {
			if username == "root" && err == ErrNoUserMap {
				// No root map available, figure out a default map
				return kernelDefaultMap()
			}

			return nil, err
		}

		for _, entry := range entries {
			// Check that it's big enough to be useful
			if int(entry[1]) < 65536 {
				continue
			}

			e := IdmapEntry{Isgid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)

			// NOTE: Remove once we can deal with multiple shadow maps
			break
		}

		return idmapset, nil
	}

	// No shadow available, figure out a default map
	return kernelDefaultMap()
}

func kernelDefaultMap() (*IdmapSet, error) {
	idmapset := new(IdmapSet)

	kernelMap, err := CurrentIdmapSet()
	if err != nil {
		// Hardcoded fallback map
		e := IdmapEntry{Isuid: true, Isgid: false, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = Extend(idmapset.Idmap, e)

		e = IdmapEntry{Isuid: false, Isgid: true, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
		return idmapset, nil
	}

	// Look for mapped ranges
	kernelRanges, err := kernelMap.ValidRanges()
	if err != nil {
		return nil, err
	}

	// Special case for when we have the full kernel range
	fullKernelRanges := []*IdRange{
		{true, false, int64(0), int64(4294967294)},
		{false, true, int64(0), int64(4294967294)}}

	if reflect.DeepEqual(kernelRanges, fullKernelRanges) {
		// Hardcoded fallback map
		e := IdmapEntry{Isuid: true, Isgid: false, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = Extend(idmapset.Idmap, e)

		e = IdmapEntry{Isuid: false, Isgid: true, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
		return idmapset, nil
	}

	// Find a suitable uid range
	for _, entry := range kernelRanges {
		// We only care about uids right now
		if !entry.Isuid {
			continue
		}

		// We want a map that's separate from the system's own POSIX allocation
		if entry.Endid < 100000 {
			continue
		}

		// Don't use the first 65536 ids
		if entry.Startid < 100000 {
			entry.Startid = 100000
		}

		// Check if we have enough ids
		if entry.Endid-entry.Startid < 65536 {
			continue
		}

		// Add the map
		e := IdmapEntry{Isuid: true, Isgid: false, Nsid: 0, Hostid: entry.Startid, Maprange: entry.Endid - entry.Startid + 1}
		idmapset.Idmap = Extend(idmapset.Idmap, e)

		// NOTE: Remove once we can deal with multiple shadow maps
		break
	}

	// Find a suitable gid range
	for _, entry := range kernelRanges {
		// We only care about gids right now
		if !entry.Isgid {
			continue
		}

		// We want a map that's separate from the system's own POSIX allocation
		if entry.Endid < 100000 {
			continue
		}

		// Don't use the first 65536 ids
		if entry.Startid < 100000 {
			entry.Startid = 100000
		}

		// Check if we have enough ids
		if entry.Endid-entry.Startid < 65536 {
			continue
		}

		// Add the map
		e := IdmapEntry{Isuid: false, Isgid: true, Nsid: 0, Hostid: entry.Startid, Maprange: entry.Endid - entry.Startid + 1}
		idmapset.Idmap = Extend(idmapset.Idmap, e)

		// NOTE: Remove once we can deal with multiple shadow maps
		break
	}

	return idmapset, nil
}

/*
 * Create an idmap of the current allocation.
 */
func CurrentIdmapSet() (*IdmapSet, error) {
	idmapset := new(IdmapSet)

	if util.PathExists("/proc/self/uid_map") {
		// Parse the uidmap
		entries, err := getFromProc("/proc/self/uid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := IdmapEntry{Isuid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)
		}
	} else {
		// Fallback map
		e := IdmapEntry{Isuid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
	}

	if util.PathExists("/proc/self/gid_map") {
		// Parse the gidmap
		entries, err := getFromProc("/proc/self/gid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := IdmapEntry{Isgid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)
		}
	} else {
		// Fallback map
		e := IdmapEntry{Isgid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
	}

	return idmapset, nil
}

// JSONUnmarshal unmarshals an IDMAP encoded as JSON.
func JSONUnmarshal(idmapJSON string) (*IdmapSet, error) {
	lastIdmap := new(IdmapSet)
	err := json.Unmarshal([]byte(idmapJSON), &lastIdmap.Idmap)
	if err != nil {
		return nil, err
	}

	if len(lastIdmap.Idmap) == 0 {
		return nil, nil
	}

	return lastIdmap, nil
}

// JSONMarshal marshals an IDMAP to JSON string.
func JSONMarshal(idmapSet *IdmapSet) (string, error) {
	idmapBytes, err := json.Marshal(idmapSet.Idmap)
	if err != nil {
		return "", err
	}

	return string(idmapBytes), nil
}

// GetIdmapSet reads the uid/gid allocation.
func GetIdmapSet() *IdmapSet {
	idmapSet, err := DefaultIdmapSet("", "")
	if err != nil {
		logger.Warn("Error reading default uid/gid map", map[string]any{"err": err.Error()})
		logger.Warnf("Only privileged containers will be able to run")
		idmapSet = nil
	} else {
		kernelIdmapSet, err := CurrentIdmapSet()
		if err == nil {
			logger.Infof("Kernel uid/gid map:")
			for _, lxcmap := range kernelIdmapSet.ToLxcString() {
				logger.Infof(fmt.Sprintf(" - %s", lxcmap))
			}
		}

		if len(idmapSet.Idmap) == 0 {
			logger.Warnf("No available uid/gid map could be found")
			logger.Warnf("Only privileged containers will be able to run")
			idmapSet = nil
		} else {
			logger.Infof("Configured uid/gid map:")
			for _, lxcmap := range idmapSet.Idmap {
				suffix := ""

				if lxcmap.Usable() != nil {
					suffix = " (unusable)"
				}

				for _, lxcEntry := range lxcmap.ToLxcString() {
					logger.Infof(" - %s%s", lxcEntry, suffix)
				}
			}

			err = idmapSet.Usable()
			if err != nil {
				logger.Warnf("One or more uid/gid map entry isn't usable (typically due to nesting)")
				logger.Warnf("Only privileged containers will be able to run")
				idmapSet = nil
			}
		}
	}

	return idmapSet
}
