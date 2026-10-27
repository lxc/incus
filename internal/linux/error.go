package linux

import (
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// GetErrno checks if the Go error is a kernel errno.
func GetErrno(err error) (iserrno bool, errno error) {
	sysErr, ok := errors.AsType[*os.SyscallError](err)
	if ok {
		return true, sysErr.Err
	}

	pathErr, ok := errors.AsType[*os.PathError](err)
	if ok {
		return true, pathErr.Err
	}

	tmpErrno, ok := errors.AsType[unix.Errno](err)
	if ok {
		return true, tmpErrno
	}

	return false, nil
}

// ExitStatus extracts the exit status from the error returned by exec.Cmd.
// If a nil err is provided then an exit status of 0 is returned along with the nil error.
// If a valid exit status can be extracted from err then it is returned along with a nil error.
// If no valid exit status can be extracted then a -1 exit status is returned along with the err provided.
func ExitStatus(err error) (int, error) {
	if err == nil {
		return 0, err // No error exit status.
	}

	// Detect and extract ExitError to check the embedded exit status.
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if ok {
		// If the process was signaled, extract the signal.
		status, isWaitStatus := exitErr.Sys().(unix.WaitStatus)
		if isWaitStatus && status.Signaled() {
			return 128 + int(status.Signal()), nil // 128 + n == Fatal error signal "n"
		}

		// Otherwise capture the exit status from the command.
		return exitErr.ExitCode(), nil
	}

	return -1, err // Not able to extract an exit status.
}
