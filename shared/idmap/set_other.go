//go:build !linux || !cgo

package idmap

import (
	"fmt"
)

// ErrNoIdmapSupport is an indicating that the host os does not support idmaps
var ErrNoIdmapSupport = fmt.Errorf("No idmap support for non-linux hosts")

// GetSet reads the uid/gid allocation.
func GetSet() *Set {
	return nil
}

// CurrentSet creates an idmap of the current allocation.
func CurrentSet() (*Set, error) {
	return nil, ErrNoIdmapSupport
}

// DefaultSet creates a new default idmap.
func DefaultSet(rootfs string, username string) (*Set, error) {
	return nil, ErrNoIdmapSupport
}
