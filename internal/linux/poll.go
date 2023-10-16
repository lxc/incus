package linux

import (
	"golang.org/x/sys/unix"
)

// GetPollRevents poll for events on provided fd.
func GetPollRevents(fd int, timeout int, flags int) (int, int, error) {
	pollFd := unix.PollFd{
		Fd:      int32(fd),
		Events:  int16(flags),
		Revents: 0,
	}

	pollFds := []unix.PollFd{pollFd}

again:
	n, err := unix.Poll(pollFds, timeout)
	if err != nil {
		if err == unix.EAGAIN || err == unix.EINTR {
			goto again
		}

		return -1, -1, err
	}

	return n, int(pollFds[0].Revents), err
}
