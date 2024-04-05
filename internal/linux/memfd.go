package linux

import (
	"os"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/revert"
)

// CreateMemfd creates a new memfd for the provided byte slice.
func CreateMemfd(content []byte) (*os.File, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the memfd.
	fd, err := unix.MemfdCreate("memfd", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { unix.Close(fd) })

	// Set its size.
	err = unix.Ftruncate(fd, int64(len(content)))
	if err != nil {
		return nil, err
	}

	// Prepare the storage.
	data, err := unix.Mmap(fd, 0, len(content), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	// Write the content.
	copy(data, content)

	// Cleanup.
	err = unix.Munmap(data)
	if err != nil {
		return nil, err
	}

	revert.Success()
	return os.NewFile(uintptr(fd), "memfd"), nil
}
