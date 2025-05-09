package util

import (
	"github.com/lxc/incus/v6/shared/util"
)

// IsIncusOS checks if the host system is running Incus OS.
func IsIncusOS() bool {
	return util.PathExists("/run/incus-os/unix.socket")
}
