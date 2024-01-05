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

const (
	// VFS3FSCapsUnknown indicates unknown support for VFS v3 fscaps.
	VFS3FSCapsUnknown = int32(-1)

	// VFS3FSCapsUnsupported indicates the kernel does not support VFS v3 fscaps.
	VFS3FSCapsUnsupported = int32(0)

	// VFS3FSCapsSupported indicates the kernel supports VFS v3 fscaps.
	VFS3FSCapsSupported = int32(1)
)

// VFS3FSCaps can be set to tell the shifter if VFS v3 fscaps are supported.
var VFS3FSCaps = VFS3FSCapsUnknown

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
			for _, lxcmap := range kernelSet.ToLXCString() {
				logger.Infof(fmt.Sprintf(" - %s", lxcmap))
			}
		}

		if len(idmapSet.Entries) == 0 {
			logger.Warnf("No available uid/gid map could be found")
			logger.Warnf("Only privileged containers will be able to run")
			idmapSet = nil
		} else {
			logger.Infof("Configured uid/gid map:")
			for _, lxcmap := range idmapSet.Entries {
				suffix := ""

				if lxcmap.Usable() != nil {
					suffix = " (unusable)"
				}

				for _, lxcEntry := range lxcmap.ToLXCString() {
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
			e := Entry{IsUID: true, NSID: entry[0], HostID: entry[1], MapRange: entry[2]}
			idmapset.Entries = append(idmapset.Entries, e)
		}
	} else {
		// Fallback map.
		e := Entry{IsUID: true, NSID: 0, HostID: 0, MapRange: 0}
		idmapset.Entries = append(idmapset.Entries, e)
	}

	if util.PathExists("/proc/self/gid_map") {
		// Parse the gidmap.
		entries, err := getFromProc("/proc/self/gid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := Entry{IsGID: true, NSID: entry[0], HostID: entry[1], MapRange: entry[2]}
			idmapset.Entries = append(idmapset.Entries, e)
		}
	} else {
		// Fallback map.
		e := Entry{IsGID: true, NSID: 0, HostID: 0, MapRange: 0}
		idmapset.Entries = append(idmapset.Entries, e)
	}

	return idmapset, nil
}

// ShiftIntoContainer shiftfs a host filesystem tree.
func (m *Set) ShiftIntoContainer(dir string, testmode bool) error {
	return m.doShiftIntoContainer(dir, testmode, "in", nil)
}

// ShiftFromContainer shiftfs a container filesystem tree.
func (m *Set) ShiftFromContainer(dir string, testmode bool) error {
	return m.doShiftIntoContainer(dir, testmode, "out", nil)
}

// ShiftRootfs shiftfs a whole container filesystem tree.
func (m *Set) ShiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return m.doShiftIntoContainer(p, false, "in", skipper)
}

// UnshiftRootfs unshiftfs a whole container filesystem tree.
func (m *Set) UnshiftRootfs(p string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	return m.doShiftIntoContainer(p, false, "out", skipper)
}

// ShiftFile shiftfs a single file.
func (m *Set) ShiftFile(p string) error {
	return m.ShiftRootfs(p, nil)
}

// ToUIDMappings converts an idmapset to a slice of syscall.SysProcIDMap.
func (m Set) ToUIDMappings() []syscall.SysProcIDMap {
	mapping := []syscall.SysProcIDMap{}

	for _, e := range m.Entries {
		if !e.IsUID {
			continue
		}

		mapping = append(mapping, syscall.SysProcIDMap{
			ContainerID: int(e.NSID),
			HostID:      int(e.HostID),
			Size:        int(e.MapRange),
		})
	}

	return mapping
}

// ToGIDMappings converts an idmapset to a slice of syscall.SysProcIDMap.
func (m Set) ToGIDMappings() []syscall.SysProcIDMap {
	mapping := []syscall.SysProcIDMap{}

	for _, e := range m.Entries {
		if !e.IsGID {
			continue
		}

		mapping = append(mapping, syscall.SysProcIDMap{
			ContainerID: int(e.NSID),
			HostID:      int(e.HostID),
			Size:        int(e.MapRange),
		})
	}

	return mapping
}

func (m *Set) doShiftIntoContainer(dir string, testmode bool, how string, skipper func(dir string, absPath string, fi os.FileInfo) bool) error {
	if how == "in" && atomic.LoadInt32(&VFS3FSCaps) == VFS3FSCapsUnknown {
		if SupportsVFS3FSCaps(dir) {
			atomic.StoreInt32(&VFS3FSCaps, VFS3FSCapsSupported)
		} else {
			atomic.StoreInt32(&VFS3FSCaps, VFS3FSCapsUnsupported)
		}
	}

	// Expand any symlink before the final path component.
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
				// File was already shifted through hardlink.
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
			newuid, newgid = m.ShiftIntoNS(uid, gid)
		case "out":
			newuid, newgid = m.ShiftFromNS(uid, gid)
		}

		if testmode {
			fmt.Printf("I would shift %q to %d %d\n", p, newuid, newgid)
		} else {
			// Dump capabilities.
			if fi.Mode()&os.ModeSymlink == 0 {
				caps, err = GetCaps(p)
				if err != nil {
					return err
				}
			}

			// Shift owner.
			err = ShiftOwner(dir, p, int(newuid), int(newgid))
			if err != nil {
				return err
			}

			if fi.Mode()&os.ModeSymlink == 0 {
				// Shift POSIX ACLs.
				err = ShiftACL(p, func(uid int64, gid int64) (int64, int64) { return m.doShiftIntoNS(uid, gid, how) })
				if err != nil {
					return err
				}

				// Shift capabilities.
				if len(caps) != 0 {
					rootUID := int64(0)
					if how == "in" {
						rootUID, _ = m.ShiftIntoNS(0, 0)
					}

					if how != "in" || atomic.LoadInt32(&VFS3FSCaps) == VFS3FSCapsSupported {
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

	kernelMap, err := CurrentSet()
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
