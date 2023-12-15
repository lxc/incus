//go:build !linux || !cgo

package idmap

import (
	"fmt"
)

// ErrNoIdmapSupport is an indicating that the host os does not support idmaps
var ErrNoIdmapSupport = fmt.Errorf("No idmap support for non-linux hosts")

// GetIdmapSet reads the uid/gid allocation.
func GetIdmapSet() *IdmapSet {
	return nil
}

// CurrentIdmapSet creates an idmap of the current allocation.
func CurrentIdmapSet() (*IdmapSet, error) {
	return nil, ErrNoIdmapSupport
}

// DefaultIdmapSet creates a new default idmap.
func DefaultIdmapSet(rootfs string, username string) (*IdmapSet, error) {
	return nil, ErrNoIdmapSupport
}
