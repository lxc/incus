//go:build linux && cgo

package subprocess

import (
	"syscall"
)

// SetUserns allows running inside of a user namespace.
func (p *Process) SetUserns(uidMap []syscall.SysProcIDMap, gidMap []syscall.SysProcIDMap) {
	p.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
		Credential: &syscall.Credential{
			Uid: uint32(0),
			Gid: uint32(0),
		},
		UidMappings: uidMap,
		GidMappings: gidMap,
	}
}
