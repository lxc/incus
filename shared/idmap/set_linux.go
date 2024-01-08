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

// ShiftSkipper is a function used to skip shifting or unshifting specific paths.
type ShiftSkipper func(dir string, absPath string, fi os.FileInfo, newuid int64, newgid int64) error

// ShiftPath shifts a whole filesystem tree.
func (m *Set) ShiftPath(p string, skipper ShiftSkipper) error {
	return m.doShiftIntoContainer(p, "in", skipper)
}

// UnshiftPath unshifts a whole filesystem tree.
func (m *Set) UnshiftPath(p string, skipper ShiftSkipper) error {
	return m.doShiftIntoContainer(p, "out", skipper)
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

func (m *Set) doShiftIntoContainer(dir string, how string, skipper ShiftSkipper) error {
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

		// Handle skipping.
		if skipper != nil {
			err := skipper(dir, p, fi, newuid, newgid)
			// Pass through SkipAll and SkipDir.
			if err == filepath.SkipAll || err == filepath.SkipDir {
				return err
			}

			// All other errors result in simple file skipping.
			if err != nil {
				return nil
			}
		}

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

		return nil
	}

	if !util.PathExists(dir) {
		return fmt.Errorf("No such file or directory: %q", dir)
	}

	return filepath.Walk(dir, convert)
}
