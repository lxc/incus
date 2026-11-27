//go:build linux && cgo

package instancewriter

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/linux"
	"github.com/lxc/incus/v7/shared/idmap"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

// linkMapMaxSize bounds the memory spent tracking hard links, past it links are stored as plain files.
const linkMapMaxSize = 64 * 1024 * 1024

// linkMapEntrySize is the estimated overhead of a link map entry beyond its name.
const linkMapEntrySize = 64

// InstanceTarWriter provides an InstanceWriter implementation that handles ID shifting and hardlink tracking.
type InstanceTarWriter struct {
	tarWriter   *tar.Writer
	idmapSet    *idmap.Set
	linkMap     map[uint64]string
	linkMapSize int
}

// NewInstanceTarWriter returns an InstanceTarWriter for the provided target Writer and id map.
func NewInstanceTarWriter(writer io.Writer, idmapSet *idmap.Set) *InstanceTarWriter {
	ctw := &InstanceTarWriter{}
	ctw.tarWriter = tar.NewWriter(writer)
	ctw.idmapSet = idmapSet
	ctw.linkMap = map[uint64]string{}
	return ctw
}

// ResetHardLinkMap resets the hard link map. Use when copying multiple instances (or snapshots) into a tarball.
// So that the hard link map doesn't work across different instances/snapshots.
func (ctw *InstanceTarWriter) ResetHardLinkMap() {
	ctw.linkMap = map[uint64]string{}
	ctw.linkMapSize = 0
}

// WriteFile adds a file to the tarball with the specified name using the srcPath file as the contents of the file.
// The ignoreGrowth argument indicates whether to error if the srcPath file increases in size beyond the size in fi
// during the write. If false the write will return an error. If true, no error is returned, instead only the size
// specified in fi is written to the tarball. This can be used when you don't need a consistent copy of the file.
func (ctw *InstanceTarWriter) WriteFile(name string, srcPath string, fi os.FileInfo, ignoreGrowth bool) error {
	var err error
	var major, minor uint32
	var nlink int
	var ino uint64

	// Pin the entry so a path component swapped for a symlink mid-walk can't redirect the reads
	// below into the rest of the host filesystem.
	pathFd, err := unix.Open(srcPath, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("Failed to open %q: %w", srcPath, err)
	}

	defer func() { _ = unix.Close(pathFd) }()

	// Get file stat.
	var stat unix.Stat_t
	err = unix.Fstat(pathFd, &stat)
	if err != nil {
		return fmt.Errorf("Failed to get file stat: %w", err)
	}

	// Make sure we pinned the same inode the walk saw, not something swapped in since.
	walkStat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || uint64(walkStat.Dev) != uint64(stat.Dev) || walkStat.Ino != stat.Ino {
		return fmt.Errorf("File %q was replaced during export: %w", srcPath, fs.ErrNotExist)
	}

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		buf := make([]byte, unix.PathMax)
		n, err := unix.Readlinkat(pathFd, "", buf)
		if err != nil {
			return fmt.Errorf("Failed to resolve symlink for %q: %w", srcPath, err)
		}

		link = string(buf[:n])
	}

	// Sockets cannot be stored in tarballs, just skip them (consistent with tar).
	if fi.Mode()&os.ModeSocket == os.ModeSocket {
		return nil
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return fmt.Errorf("Failed to create tar info header: %w", err)
	}

	hdr.Name = name
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid = int(stat.Uid)
	hdr.Gid = int(stat.Gid)
	ino = stat.Ino
	nlink = int(stat.Nlink)

	if stat.Mode&unix.S_IFBLK != 0 || stat.Mode&unix.S_IFCHR != 0 {
		major = unix.Major(uint64(stat.Rdev))
		minor = unix.Minor(uint64(stat.Rdev))
	}

	// Unshift the id under rootfs/ for unpriv containers.
	if strings.HasPrefix(hdr.Name, "rootfs") && ctw.idmapSet != nil {
		hUID, hGID := ctw.idmapSet.ShiftFromNS(int64(hdr.Uid), int64(hdr.Gid))
		hdr.Uid = int(hUID)
		hdr.Gid = int(hGID)
		if hdr.Uid == -1 || hdr.Gid == -1 {
			return nil
		}
	}

	hdr.Devmajor = int64(major)
	hdr.Devminor = int64(minor)

	// If it's a hardlink we've already seen use the old name.
	if fi.Mode().IsRegular() && nlink > 1 {
		firstPath, found := ctw.linkMap[ino]
		if found {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = firstPath
			hdr.Size = 0
		} else if ctw.linkMapSize+len(hdr.Name)+linkMapEntrySize <= linkMapMaxSize {
			ctw.linkMap[ino] = hdr.Name
			ctw.linkMapSize += len(hdr.Name) + linkMapEntrySize
		}
	}

	// Handle xattrs (for real files only).
	if link == "" {
		xattrs, err := linux.GetAllXattrFromFd(pathFd)
		if err != nil {
			return fmt.Errorf("Failed to read xattr for %q: %w", srcPath, err)
		}

		hdr.PAXRecords = make(map[string]string, len(xattrs))
		for key, val := range xattrs {
			if key == "system.posix_acl_access" && ctw.idmapSet != nil {
				aclAccess, err := idmap.UnshiftACL(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift ACL access permissions of %q: %v", srcPath, err)
					continue
				}

				val = aclAccess
			} else if key == "system.posix_acl_default" && ctw.idmapSet != nil {
				aclDefault, err := idmap.UnshiftACL(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift ACL default permissions of %q: %v", srcPath, err)
					continue
				}

				val = aclDefault
			} else if key == "security.capability" && ctw.idmapSet != nil {
				vfsCaps, err := idmap.UnshiftCaps(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift VFS capabilities of %q: %v", srcPath, err)
					continue
				}

				val = vfsCaps
			}

			hdr.PAXRecords["SCHILY.xattr."+key] = val
		}
	}

	err = ctw.tarWriter.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("Failed to write tar header: %w", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", pathFd))
		if err != nil {
			return fmt.Errorf("Failed to open file %q: %w", srcPath, err)
		}

		defer logger.WarnOnErrorExcept(f.Close, []error{os.ErrClosed}, "Failed to close file", logger.Ctx{"path": srcPath})

		r := io.Reader(f)
		if ignoreGrowth {
			r = io.LimitReader(r, fi.Size())
		}

		_, err = util.SafeCopy(ctw.tarWriter, r)
		if err != nil {
			return fmt.Errorf("Failed to copy file content %q: %w", srcPath, err)
		}

		err = f.Close()
		if err != nil {
			return fmt.Errorf("Failed to close file %q: %w", srcPath, err)
		}
	}

	return nil
}

// WriteFileFromReader streams a file into the tarball using the src reader.
// A manually generated os.FileInfo should be supplied so that the tar header can be added before streaming starts.
func (ctw *InstanceTarWriter) WriteFileFromReader(src io.Reader, fi os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return fmt.Errorf("Failed to create tar info header: %w", err)
	}

	err = ctw.tarWriter.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("Failed to write tar header: %w", err)
	}

	_, err = util.SafeCopy(ctw.tarWriter, src)
	return err
}

// Close finishes writing the tarball.
func (ctw *InstanceTarWriter) Close() error {
	err := ctw.tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Failed to close tar writer: %w", err)
	}

	return nil
}
