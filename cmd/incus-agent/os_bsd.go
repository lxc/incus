//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

var (
	osBaseWorkingDirectory = "/"
	osAgentConfigPath      = "/usr/local/etc/incus-agent.yml"
	osVioSerialPath        = "/dev/virtio-ports/org.linuxcontainers.incus"
)

func runService(name string, agentCmd *cmdAgent) error {
	return errors.New("Not implemented.")
}

func parseBytes(b []byte) string {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		n = len(b)
	}

	return string(b[:n])
}

func osGetEnvironment() (*api.ServerEnvironment, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, err
	}

	env := &api.ServerEnvironment{
		Kernel:             parseBytes(uname.Sysname[:]),
		KernelArchitecture: parseBytes(uname.Machine[:]),
		KernelVersion:      parseBytes(uname.Release[:]),
		Server:             "incus-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         parseBytes(uname.Nodename[:]),
	}

	return env, nil
}

// setPtySize is the same as linux.SetPtySize for BSD-likes.
func setPtySize(fd int, width int, height int) (err error) {
	var dimensions [4]uint16
	dimensions[0] = uint16(height)
	dimensions[1] = uint16(width)

	_, _, errno := unix.Syscall6(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCSWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0)
	if errno != 0 {
		return errno
	}

	return nil
}

func osGetInteractiveConsole(s *execWs) (*os.File, *os.File, error) {
	pty, tty, err := openPty(int64(s.uid), int64(s.gid))
	if err != nil {
		return nil, nil, err
	}

	if s.width > 0 && s.height > 0 {
		_ = setPtySize(int(pty.Fd()), s.width, s.height)
	}

	return pty, tty, nil
}

func osPrepareExecCommand(s *execWs, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: s.uid,
			Gid: s.gid,
		},
		// Creates a new session if the calling process is not a process group leader.
		// The calling process is the leader of the new session, the process group leader of
		// the new process group, and has no controlling terminal.
		// This is important to allow remote shells to handle ctrl+c.
		Setsid: true,
	}

	// Make the given terminal the controlling terminal of the calling process.
	// The calling process must be a session leader and not have a controlling terminal already.
	// This is important as allows ctrl+c to work as expected for non-shell programs.
	if s.interactive {
		cmd.SysProcAttr.Setctty = true
	}
}

func osHandleExecControl(control api.InstanceExecControl, s *execWs, pty io.ReadWriteCloser, cmd *exec.Cmd, l logger.Logger) {
	if control.Command == "window-resize" && s.interactive {
		winchWidth, err := strconv.Atoi(control.Args["width"])
		if err != nil {
			l.Debug("Unable to extract window width", logger.Ctx{"err": err})
			return
		}

		winchHeight, err := strconv.Atoi(control.Args["height"])
		if err != nil {
			l.Debug("Unable to extract window height", logger.Ctx{"err": err})
			return
		}

		osFile, ok := pty.(*os.File)
		if ok {
			err = setPtySize(int(osFile.Fd()), winchWidth, winchHeight)
			if err != nil {
				l.Debug("Failed to set window size", logger.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
				return
			}
		}
	} else if control.Command == "signal" {
		err := unix.Kill(cmd.Process.Pid, unix.Signal(control.Signal))
		if err != nil {
			l.Debug("Failed forwarding signal", logger.Ctx{"err": err, "signal": control.Signal})
			return
		}

		l.Info("Forwarded signal", logger.Ctx{"signal": control.Signal})
	}
}

// osExitStatus is is the same as linux.ExitStatus for BSD-likes.
func osExitStatus(err error) (int, error) {
	if err == nil {
		return 0, err // No error exit status.
	}

	var exitErr *exec.ExitError

	// Detect and extract ExitError to check the embedded exit status.
	if errors.As(err, &exitErr) {
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

func osGetListener(port int64) (net.Listener, error) {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on TCP: %w", err)
	}

	logger.Info("Started TCP listener")

	return l, nil
}
