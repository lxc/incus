//go:build windows

package io

import (
	"os"
	"time"
)

func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	return fInfo.Mode(), -1, -1
}

// GetUmask returns the current process umask.
func GetUmask() os.FileMode {
	return 0
}

// Lchtimes sets the access and modification times of a path.
func Lchtimes(path string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(path, atime, mtime)
}
