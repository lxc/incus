//go:build !windows

package io

import (
	"os"
	"syscall"
)

// GetOwnerMode returns the file mode, owner UID, and owner GID for the given file.
func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	mode := fInfo.Mode()
	uid := int(fInfo.Sys().(*syscall.Stat_t).Uid)
	gid := int(fInfo.Sys().(*syscall.Stat_t).Gid)
	return mode, uid, gid
}
