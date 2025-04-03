//go:build !windows

package util

import (
	"golang.org/x/sys/unix"
)

// PathIsWritable checks if the provided path is writable.
func PathIsWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}
