//go:build !windows

package util

import (
	"golang.org/x/sys/unix"
)

func PathIsWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}
