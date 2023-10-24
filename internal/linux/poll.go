package linux

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

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
	f              *os.File
	ctx            context.Context
	finishDeadline time.Time
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

			switch {
			case err != nil:
				opErr = err
			case revents&unix.POLLERR > 0:
				opErr = fmt.Errorf("Got POLLERR event")
			case revents&unix.POLLNVAL > 0:
				opErr = fmt.Errorf("Got POLLNVAL event")
			case revents&(unix.POLLIN|unix.POLLPRI) > 0:
				// If there is something to read then read it.
				n, opErr = unix.Read(int(fd), p)
				if opErr == nil && w.ctx.Err() != nil {
					if w.finishDeadline.IsZero() {
						// When the parent process finishes set a deadline to complete
						// future reads by.
						w.finishDeadline = time.Now().Add(time.Second)
					} else if time.Now().After(w.finishDeadline) {
						// If there is still output being received after the parent
						// process has finished then return EOF to prevent background
						// processes from keeping the reads ongoing.
						opErr = io.EOF
					}
				}
			case w.ctx.Err() != nil:
				// Nothing to read after process exited then return EOF.
				opErr = io.EOF
			default:
				continue
			}

			return true
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
