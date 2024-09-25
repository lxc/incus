//go:build amd64 || ppc64 || ppc64le || arm64 || s390x || mips64 || mips64le || riscv64 || loong64

package seccomp

import (
	"golang.org/x/sys/unix"
)

// ToNative fills fields from s into native fields.
func (s *Sysinfo) ToNative(n *unix.Sysinfo_t) {
	n.Bufferram = s.Bufferram / uint64(s.Unit)
	n.Freeram = s.Freeram / uint64(s.Unit)
	n.Freeswap = s.Freeswap / uint64(s.Unit)
	n.Procs = s.Procs
	n.Sharedram = s.Sharedram / uint64(s.Unit)
	n.Totalram = s.Totalram / uint64(s.Unit)
	n.Totalswap = s.Totalswap / uint64(s.Unit)
	n.Uptime = s.Uptime
	n.Unit = s.Unit
}

type sysinfo32 struct {
	Uptime    int32
	Loads     [3]uint32
	Totalram  uint32
	Freeram   uint32
	Sharedram uint32
	Bufferram uint32
	Totalswap uint32
	Freeswap  uint32
	Procs     uint16
	Pad       uint16
	Totalhigh uint32
	Freehigh  uint32
	Unit      uint32
	_         [8]int8
}

// ToNative32 returns the data as an i386 struct.
func (s *Sysinfo) ToNative32(n unix.Sysinfo_t) sysinfo32 {
	return sysinfo32{
		Uptime:    int32(s.Uptime),
		Loads:     [3]uint32{uint32(n.Loads[0]), uint32(n.Loads[1]), uint32(n.Loads[2])},
		Totalram:  uint32(s.Totalram / uint64(s.Unit)),
		Freeram:   uint32(s.Freeram / uint64(s.Unit)),
		Sharedram: uint32(s.Sharedram / uint64(s.Unit)),
		Bufferram: uint32(s.Bufferram / uint64(s.Unit)),
		Totalswap: uint32(s.Totalswap / uint64(s.Unit)),
		Freeswap:  uint32(s.Freeswap / uint64(s.Unit)),
		Procs:     uint16(s.Procs),
		Totalhigh: uint32(n.Totalhigh) * n.Unit / uint32(s.Unit),
		Freehigh:  uint32(n.Freehigh) * n.Unit / uint32(s.Unit),
		Unit:      s.Unit,
	}
}
