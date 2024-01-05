//go:build linux && cgo

package idmap

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"reflect"
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

// ErrNoUserMap indicates that no entry could be found for the user.
var ErrNoUserMap = fmt.Errorf("No map found for user")

// GetSet reads the system uid/gid allocation.
func GetSet() *Set {
	idmapSet, err := DefaultSet("", "")
	if err != nil {
		logger.Warn("Error reading default uid/gid map", map[string]any{"err": err.Error()})
		logger.Warnf("Only privileged containers will be able to run")
		idmapSet = nil
	} else {
		kernelSet, err := CurrentSet()
		if err == nil {
			logger.Infof("Kernel uid/gid map:")
			for _, lxcmap := range kernelSet.ToLxcString() {
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
			e := Entry{Isuid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = append(idmapset.Idmap, e)
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
			e := Entry{Isgid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = append(idmapset.Idmap, e)
		}

		return idmapset, nil
	}

	// No shadow available, figure out a default map.
	return kernelDefaultMap()
}

// CurrentSet returns the current process' idmapset.
func CurrentSet() (*Set, error) {
	idmapset := new(Set)

	if util.PathExists("/proc/self/uid_map") {
		// Parse the uidmap.
		entries, err := getFromProc("/proc/self/uid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := Entry{Isuid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = append(idmapset.Idmap, e)
		}
	} else {
		// Fallback map.
		e := Entry{Isuid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = append(idmapset.Idmap, e)
	}

	if util.PathExists("/proc/self/gid_map") {
		// Parse the gidmap.
		entries, err := getFromProc("/proc/self/gid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := Entry{Isgid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = append(idmapset.Idmap, e)
		}
	} else {
		// Fallback map.
		e := Entry{Isgid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = append(idmapset.Idmap, e)
	}

	return idmapset, nil
}

// UidshiftIntoContainer shiftfs a host filesystem tree.
func (m *Set) UidshiftIntoContainer(dir string, testmode bool) error {
	return m.doUidshiftIntoContainer(dir, testmode, "in", nil)
}

// UidshiftFromContainer shiftfs a container filesystem tree.
func (m *Set) UidshiftFromContainer(dir string, testmode bool) error {
	return m.doUidshiftIntoContainer(dir, testmode, "out", nil)
}

// ShiftRootfs shiftfs a whole container filesystem tree.
func (m *Set) ShiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return m.doUidshiftIntoContainer(p, false, "in", skipper)
}

// UnshiftRootfs unshiftfs a whole container filesystem tree.
func (m *Set) UnshiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return m.doUidshiftIntoContainer(p, false, "out", skipper)
}

// ShiftFile shiftfs a single file.
func (m *Set) ShiftFile(p string) error {
	return m.ShiftRootfs(p, nil)
}

// ToUidMappings converts an idmapset to a slice of syscall.SysProcIDMap.
func (m Set) ToUidMappings() []syscall.SysProcIDMap {
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

// ToGidMappings converts an idmapset to a slice of syscall.SysProcIDMap.
func (m Set) ToGidMappings() []syscall.SysProcIDMap {
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

func (m *Set) doUidshiftIntoContainer(dir string, testmode bool, how string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
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
	convert := func(p string, fi os.FileInfo, err error) (e error) {
		if err != nil {
			return err
		}

		if skipper != nil && skipper(dir, p, fi) {
			return filepath.SkipDir
		}

		var stat unix.Stat_t
		err = unix.Lstat(p, &stat)
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
			newuid, newgid = m.ShiftIntoNs(uid, gid)
		case "out":
			newuid, newgid = m.ShiftFromNs(uid, gid)
		}

		if testmode {
			fmt.Printf("I would shift %q to %d %d\n", p, newuid, newgid)
		} else {
			// Dump capabilities
			if fi.Mode()&os.ModeSymlink == 0 {
				caps, err = GetCaps(p)
				if err != nil {
					return err
				}
			}

			// Shift owner
			err = ShiftOwner(dir, p, int(newuid), int(newgid))
			if err != nil {
				return err
			}

			if fi.Mode()&os.ModeSymlink == 0 {
				// Shift POSIX ACLs
				err = ShiftACL(p, func(uid int64, gid int64) (int64, int64) { return m.doShiftIntoNs(uid, gid, how) })
				if err != nil {
					return err
				}

				// Shift capabilities
				if len(caps) != 0 {
					rootUID := int64(0)
					if how == "in" {
						rootUID, _ = m.ShiftIntoNs(0, 0)
					}

					if how != "in" || atomic.LoadInt32(&VFS3Fscaps) == VFS3FscapsSupported {
						err = SetCaps(p, caps, rootUID)
						if err != nil {
							logger.Warnf("Unable to set file capabilities on %q: %v", p, err)
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

func kernelDefaultMap() (*Set, error) {
	idmapset := new(Set)

	kernelMap, err := CurrentSet()
	if err != nil {
		// Hardcoded fallback map
		e := Entry{Isuid: true, Isgid: false, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = append(idmapset.Idmap, e)

		e = Entry{Isuid: false, Isgid: true, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = append(idmapset.Idmap, e)
		return idmapset, nil
	}

	// Look for mapped ranges
	kernelRanges, err := kernelMap.ValidRanges()
	if err != nil {
		return nil, err
	}

	// Special case for when we have the full kernel range
	fullKernelRanges := []*Range{
		{true, false, int64(0), int64(4294967294)},
		{false, true, int64(0), int64(4294967294)}}

	if reflect.DeepEqual(kernelRanges, fullKernelRanges) {
		// Hardcoded fallback map
		e := Entry{Isuid: true, Isgid: false, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = append(idmapset.Idmap, e)

		e = Entry{Isuid: false, Isgid: true, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = append(idmapset.Idmap, e)
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
		e := Entry{Isuid: true, Isgid: false, Nsid: 0, Hostid: entry.Startid, Maprange: entry.Endid - entry.Startid + 1}
		idmapset.Idmap = append(idmapset.Idmap, e)

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
		e := Entry{Isuid: false, Isgid: true, Nsid: 0, Hostid: entry.Startid, Maprange: entry.Endid - entry.Startid + 1}
		idmapset.Idmap = append(idmapset.Idmap, e)

		// NOTE: Remove once we can deal with multiple shadow maps
		break
	}

	return idmapset, nil
}
