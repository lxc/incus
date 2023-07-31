//go:build linux && cgo

package subprocess

import (
	"syscall"

	"github.com/cyphar/incus/shared/idmap"
)

// SetUserns allows running inside of a user namespace.
func (p *Process) SetUserns(userns *idmap.IdmapSet) {
	p.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
		Credential: &syscall.Credential{
			Uid: uint32(0),
			Gid: uint32(0),
		},
		UidMappings: userns.ToUidMappings(),
		GidMappings: userns.ToGidMappings(),
	}
}
