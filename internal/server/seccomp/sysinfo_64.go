//go:build amd64 || ppc64 || ppc64le || arm64 || s390x || mips64 || mips64le || riscv64 || loong64

package seccomp

import (
	"golang.org/x/sys/unix"
)

// ToNative fills fields from s into native fields.
func (s *Sysinfo) ToNative(n *unix.Sysinfo_t) {
	n.Bufferram = s.Bufferram
	n.Freeram = s.Freeram
	n.Freeswap = s.Freeswap
	n.Procs = s.Procs
	n.Sharedram = s.Sharedram
	n.Totalram = s.Totalram
	n.Totalswap = s.Totalswap
	n.Uptime = s.Uptime
}
