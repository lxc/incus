//go:build 386 || arm || ppc || s390 || mips || mipsle

package seccomp

import (
	"golang.org/x/sys/unix"
)

// ToNative fills fields from s into native fields.
func (s *Sysinfo) ToNative(n *unix.Sysinfo_t) {
	n.Bufferram = uint32(s.Bufferram)
	n.Freeram = uint32(s.Freeram)
	n.Freeswap = uint32(s.Freeswap)
	n.Procs = s.Procs
	n.Sharedram = uint32(s.Sharedram)
	n.Totalram = uint32(s.Totalram)
	n.Totalswap = uint32(s.Totalswap)
	n.Uptime = int32(s.Uptime)
}
