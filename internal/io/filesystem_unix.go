//go:build !windows

package io

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// GetOwnerMode returns the file mode, owner UID, and owner GID for the given file.
func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	mode := fInfo.Mode()
	uid := int(fInfo.Sys().(*syscall.Stat_t).Uid)
	gid := int(fInfo.Sys().(*syscall.Stat_t).Gid)
	return mode, uid, gid
}

// GetUmask returns the current process umask.
func GetUmask() os.FileMode {
	mask := syscall.Umask(0)
	syscall.Umask(mask)

	return os.FileMode(mask)
}

// Lchtimes sets the access and modification times of a path without following symlinks.
func Lchtimes(path string, atime time.Time, mtime time.Time) error {
	return unix.Lutimes(path, []unix.Timeval{unix.NsecToTimeval(atime.UnixNano()), unix.NsecToTimeval(mtime.UnixNano())})
}
