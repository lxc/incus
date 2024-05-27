//go:build 386 || arm || ppc || s390 || mips || mipsle

package seccomp

import (
	"golang.org/x/sys/unix"
)

// ToNative fills fields from s into native fields.
func (s *Sysinfo) ToNative(n *unix.Sysinfo_t) {
	n.Bufferram = uint32(s.Bufferram / s.Unit)
	n.Freeram = uint32(s.Freeram / s.Unit)
	n.Freeswap = uint32(s.Freeswap / s.Unit)
	n.Procs = s.Procs
	n.Sharedram = uint32(s.Sharedram / s.Unit)
	n.Totalram = uint32(s.Totalram / s.Unit)
	n.Totalswap = uint32(s.Totalswap / s.Unit)
	n.Uptime = int32(s.Uptime)
	n.Unit = uint32(s.Unit)
}

// ToNative32 should never be called on 32bit arches.
func (s *Sysinfo) ToNative32(n unix.Sysinfo_t) *unix.Sysinfo_t {
	return nil
}
