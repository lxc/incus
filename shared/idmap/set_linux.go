//go:build linux && cgo

package idmap

import (
	"fmt"
	"os"
	"path/filepath"
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
func (m *Set) ToUIDMappings() []syscall.SysProcIDMap {
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
func (m *Set) ToGIDMappings() []syscall.SysProcIDMap {
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
