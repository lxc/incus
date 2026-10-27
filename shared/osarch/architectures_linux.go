//go:build linux

package osarch

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// ArchitectureGetLocal returns the local hardware architecture.
func ArchitectureGetLocal() (string, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return ArchitectureDefault, err
	}

	return string(uname.Machine[:bytes.IndexByte(uname.Machine[:], 0)]), nil
}
