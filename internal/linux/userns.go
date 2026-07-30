package linux

import (
	"syscall"
)

// See PROC_USER_INIT_INO in include/uapi/linux/nsfs.h.
const procUserInitIno = 0xEFFFFFFD

// RunningInUserNS returns true if the current process is running inside a user namespace.
func RunningInUserNS() bool {
	var st syscall.Stat_t

	err := syscall.Stat("/proc/self/ns/user", &st)
	if err != nil {
		return false
	}

	return st.Ino != procUserInitIno
}
