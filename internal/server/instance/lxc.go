//go:build linux && cgo

package instance

import (
	"fmt"
	"path/filepath"

	liblxc "github.com/lxc/go-lxc"

	"github.com/lxc/incus/v7/shared/util"
)

// NewLXCContainer returns the go-lxc struct for the named container.
// On instantiation, liblxc loads <lxcPath>/<name>/config and acts on <lxcPath>/<name>/partial by
// itself. Incus never creates either, and that directory is the instance volume, so an image could
// plant them; refuse to hand such a directory to liblxc.
func NewLXCContainer(name string, lxcPath string) (*liblxc.Container, error) {
	for _, entry := range []string{"config", "partial"} {
		if util.PathExists(filepath.Join(lxcPath, name, entry)) {
			return nil, fmt.Errorf("Unexpected %q entry in the directory of container %q", entry, name)
		}
	}

	return liblxc.NewContainer(name, lxcPath)
}
