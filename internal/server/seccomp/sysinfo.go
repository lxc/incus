package seccomp

// Sysinfo architecture independent sysinfo struct.
type Sysinfo struct {
	Uptime    int64
	Totalram  uint64
	Freeram   uint64
	Sharedram uint64
	Bufferram uint64
	Totalswap uint64
	Freeswap  uint64
	Procs     uint16
	Unit      uint32
}
