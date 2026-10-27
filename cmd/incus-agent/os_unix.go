//go:build darwin || freebsd || linux || netbsd

package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	osShutdownSignal       = unix.SIGTERM
	osBaseWorkingDirectory = "/"
)

func runService(name string, agentCmd *cmdAgent) error {
	return errors.New("Not implemented.")
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

func osStartExecCommand(ctx context.Context, cmd *exec.Cmd, pty io.ReadWriteCloser) (execProcess, error) {
	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return &cmdProcess{cmd: cmd}, nil
}
