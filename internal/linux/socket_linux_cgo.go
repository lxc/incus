//go:build linux && cgo

package linux

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <poll.h>
#include <pty.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/un.h>

#include "../../shared/cgo/process_utils.h"
#include "../../shared/cgo/syscall_wrappers.h"

#define ABSTRACT_UNIX_SOCK_LEN sizeof(((struct sockaddr_un *)0)->sun_path)

static int read_pid(int fd)
{
	ssize_t ret;
	pid_t n = -1;

again:
	ret = read(fd, &n, sizeof(n));
	if (ret < 0 && errno == EINTR)
		goto again;

	if (ret < 0)
		return -1;

	return n;
}
*/
import "C"

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	_ "github.com/lxc/incus/v7/shared/cgo" // Used by cgo
)

// ABSTRACT_UNIX_SOCK_LEN is the maximum length of an abstract unix socket path.
const ABSTRACT_UNIX_SOCK_LEN int = C.ABSTRACT_UNIX_SOCK_LEN

// ReadPid reads a PID from the provided file.
func ReadPid(r *os.File) int {
	return int(C.read_pid(C.int(r.Fd())))
}

func unCloexec(fd int) error {
	var err error
	flags, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), unix.F_GETFD, 0)
	if errno != 0 {
		err = errno
		return err
	}

	flags &^= unix.FD_CLOEXEC
	_, _, errno = unix.Syscall(unix.SYS_FCNTL, uintptr(fd), unix.F_SETFD, flags)
	if errno != 0 {
		err = errno
	}

	return err
}

// PidFdOpen opens a pidfd referring to the process with the given PID.
func PidFdOpen(Pid int, Flags uint32) (*os.File, error) {
	pidFd, errno := C.incus_pidfd_open(C.int(Pid), C.uint32_t(Flags))
	if errno != nil {
		return nil, errno
	}

	errno = unCloexec(int(pidFd))
	if errno != nil {
		return nil, errno
	}

	return os.NewFile(uintptr(pidFd), fmt.Sprintf("%d", Pid)), nil
}

// PidfdSendSignal sends a signal to the process referred to by the given pidfd.
func PidfdSendSignal(Pidfd int, Signal int, Flags uint32) error {
	ret, errno := C.incus_pidfd_send_signal(C.int(Pidfd), C.int(Signal), nil, C.uint32_t(Flags))
	if ret != 0 {
		return errno
	}

	return nil
}

const (
	// CLOSE_RANGE_UNSHARE matches CLOSE_RANGE_UNSHARE flag.
	CLOSE_RANGE_UNSHARE uint32 = C.CLOSE_RANGE_UNSHARE

	// CLOSE_RANGE_CLOEXEC matches CLOSE_RANGE_CLOEXEC flag.
	CLOSE_RANGE_CLOEXEC uint32 = C.CLOSE_RANGE_CLOEXEC
)

// CloseRange closes all file descriptors in the given range.
func CloseRange(FirstFd uint32, LastFd uint32, Flags uint32) error {
	ret, errno := C.incus_close_range(C.uint32_t(FirstFd), C.uint32_t(LastFd), C.uint32_t(Flags))
	if ret != 0 {
		if errno != unix.ENOSYS && errno != unix.EINVAL {
			return errno
		}
	}

	return nil
}
