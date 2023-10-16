package linux

import (
	"context"
	"fmt"
	"io"
	"os"

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

// NewExecWrapper returns a new ReadWriteCloser wrapper for an os.File.
// The ctx is used to indicate when the executed process has ended, at which point any further Read calls will
// return io.EOF rather than potentially blocking on the poll syscall if the process is a shell that still has
// background processes running that are not producing any output.
func NewExecWrapper(ctx context.Context, f *os.File) io.ReadWriteCloser {
	return &execWrapper{
		ctx: ctx,
		f:   f,
	}
}

// execWrapper implements a ReadWriteCloser wrapper for an os.File connected to a PTY.
type execWrapper struct {
	f      *os.File
	ctx    context.Context
	hangup bool
}

// Read uses the poll syscall with a timeout of 1s to check if there is any data to read.
// This avoids potentially blocking in the poll syscall in situations where the process is a shell that has
// background processes that are not producing any output.
// If the ctx has been cancelled before the poll starts then io.EOF error is returned.
func (w *execWrapper) Read(p []byte) (int, error) {
	rawConn, err := w.f.SyscallConn()
	if err != nil {
		return 0, err
	}

	var opErr error
	var n int
	err = rawConn.Read(func(fd uintptr) bool {
		for {
			// Call poll() with 1s timeout, this prevents blocking if a shell process exits leaving
			// background processes running that are not outputting anything.
			_, revents, err := GetPollRevents(int(fd), 1000, (unix.POLLIN | unix.POLLPRI | unix.POLLERR | unix.POLLNVAL | unix.POLLHUP | unix.POLLRDHUP))

			if revents&(unix.POLLHUP|unix.POLLRDHUP) > 0 {
				w.hangup = true // Record a hangup event if seen.
			}

			if err != nil {
				opErr = err
				return true
			} else if revents&unix.POLLERR > 0 {
				opErr = fmt.Errorf("Got POLLERR event")
				return true
			} else if revents&unix.POLLNVAL > 0 {
				opErr = fmt.Errorf("Got POLLNVAL event")
				return true
			} else if !w.hangup && w.ctx.Err() != nil {
				// If we've not seen a hangup event and context has been cancelled then return EOF.
				// This ensures that if there is a background process generating lots of output it
				// doesn't block the session from finishing when the process has ended.
				opErr = io.EOF
				return true
			} else if revents&(unix.POLLIN|unix.POLLPRI) > 0 {
				// If there is something to read then read it.
				n, opErr = unix.Read(int(fd), p)
				return true
			} else if w.hangup {
				// If we've seen a hangup event and there's nothing to read then return EOF.
				opErr = io.EOF
				return true
			}
		}
	})
	if err != nil {
		return n, err
	}

	return n, opErr
}

func (w *execWrapper) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

func (w *execWrapper) Close() error {
	return w.f.Close()
}
