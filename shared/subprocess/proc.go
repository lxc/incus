//go:build !windows

package subprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

// Process struct. Has ability to set runtime arguments.
type Process struct {
	exitCode int64
	exitErr  error

	chExit     chan struct{}
	hasMonitor bool
	closeFds   bool
	process    *os.Process

	Name      string         `yaml:"name"`
	Args      []string       `yaml:"args,flow"`
	Apparmor  string         `yaml:"apparmor"`
	Cwd       string         `yaml:"cwd"`
	PID       int64          `yaml:"pid"`
	StartTime uint64         `yaml:"start_time,omitempty"`
	BootID    string         `yaml:"boot_id,omitempty"`
	Stdin     io.ReadCloser  `yaml:"-"`
	Stdout    io.WriteCloser `yaml:"-"`
	Stderr    io.WriteCloser `yaml:"-"`

	UID       uint32 `yaml:"uid"`
	GID       uint32 `yaml:"gid"`
	SetGroups bool   `yaml:"set_groups"`

	SysProcAttr *syscall.SysProcAttr
}

func (p *Process) hasApparmor() bool {
	if util.IsFalse(os.Getenv("INCUS_SECURITY_APPARMOR")) {
		return false
	}

	_, err := exec.LookPath("aa-exec")
	if err != nil {
		return false
	}

	if !util.PathExists("/sys/kernel/security/apparmor") {
		return false
	}

	return true
}

// bootID returns the current kernel boot ID (empty when unavailable).
func bootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// procStartTime returns the start time (in clock ticks since boot) of a process.
func procStartTime(pid int64) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	// The command name is enclosed in parentheses and may contain spaces.
	_, after, found := strings.Cut(string(data), ") ")
	if !found {
		return 0, fmt.Errorf("Invalid stat content for process %d", pid)
	}

	// Start time is the 22nd field (index 19 after the command name).
	fields := strings.Fields(after)
	if len(fields) < 20 {
		return 0, fmt.Errorf("Invalid stat content for process %d", pid)
	}

	return strconv.ParseUint(fields[19], 10, 64)
}

// matches checks that the process currently using the PID is the one that was recorded.
func (p *Process) matches() bool {
	// Without a boot ID (non-Linux), there is no way to verify the process identity.
	bootID := bootID()
	if bootID == "" {
		return true
	}

	// Verify the start time and boot ID when they were recorded.
	if p.StartTime != 0 {
		if p.BootID != bootID {
			return false
		}

		startTime, err := procStartTime(p.PID)
		if err != nil {
			return false
		}

		return startTime == p.StartTime
	}

	// Fall back to comparing the command line for files written by older versions.
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p.PID))
	if err != nil {
		return false
	}

	cmdline := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	if len(cmdline) == 0 || filepath.Base(cmdline[0]) != filepath.Base(p.Name) {
		return false
	}

	return slices.Equal(cmdline[1:], p.Args)
}

// findProcess returns a handle to the process, checking it's the one we recorded.
func (p *Process) findProcess() (*os.Process, error) {
	// Use the handle from Start when available as it can't be subject to PID reuse.
	if p.process != nil {
		return p.process, nil
	}

	if p.PID <= 0 {
		return nil, ErrNotRunning
	}

	// Get a handle (pidfd on Linux) before checking so the identity can't change afterwards.
	pr, err := os.FindProcess(int(p.PID))
	if err != nil {
		if err == os.ErrProcessDone {
			return nil, ErrNotRunning
		}

		return nil, err
	}

	if !p.matches() {
		return nil, ErrNotRunning
	}

	return pr, nil
}

// GetPid returns the pid for the given process object.
func (p *Process) GetPid() (int64, error) {
	pr, err := p.findProcess()
	if err != nil {
		return 0, err
	}

	err = pr.Signal(syscall.Signal(0))
	if err != nil {
		if err == os.ErrProcessDone {
			return 0, ErrNotRunning
		}

		return 0, err
	}

	return p.PID, nil
}

// SetApparmor allows setting the AppArmor profile.
func (p *Process) SetApparmor(profile string) {
	p.Apparmor = profile
}

// SetCreds allows setting process credentials.
func (p *Process) SetCreds(uid uint32, gid uint32) {
	p.UID = uid
	p.GID = gid
}

// Stop will stop the given process object.
func (p *Process) Stop() error {
	pr, err := p.findProcess()
	if err != nil {
		if errors.Is(err, ErrNotRunning) && p.hasMonitor {
			<-p.chExit
		}

		return err
	}

	// Check if process exists.
	err = pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Kill()
		if err == nil {
			if p.hasMonitor {
				<-p.chExit
			}

			return nil // Killed successfully.
		}
	}

	// Check if either the existence check or the kill resulted in an already finished error.
	if err == os.ErrProcessDone {
		if p.hasMonitor {
			<-p.chExit
		}

		return ErrNotRunning
	}

	return fmt.Errorf("Could not kill process: %w", err)
}

// Start will start the given process object.
func (p *Process) Start(ctx context.Context) error {
	return p.start(ctx, nil)
}

// StartWithFiles will start the given process object with extra file descriptors.
func (p *Process) StartWithFiles(ctx context.Context, fds []*os.File) error {
	return p.start(ctx, fds)
}

func (p *Process) start(ctx context.Context, fds []*os.File) error {
	var cmd *exec.Cmd

	if p.Apparmor != "" && p.hasApparmor() {
		cmd = exec.CommandContext(ctx, "aa-exec", append([]string{"-p", p.Apparmor, p.Name}, p.Args...)...)
	} else {
		cmd = exec.CommandContext(ctx, p.Name, p.Args...)
	}

	cmd.Stdout = p.Stdout
	cmd.Stderr = p.Stderr
	cmd.Stdin = p.Stdin
	cmd.SysProcAttr = p.SysProcAttr

	if p.Cwd != "" {
		cmd.Dir = p.Cwd
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setsid = true

	if p.UID != 0 || p.GID != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{}
		cmd.SysProcAttr.Credential.Uid = p.UID
		cmd.SysProcAttr.Credential.Gid = p.GID
	}

	if fds != nil {
		cmd.ExtraFiles = fds
	}

	if p.Stdout != nil && p.closeFds {
		defer logger.WarnOnError(p.Stdout.Close, "Failed to close stdout")
	}

	if p.Stderr != nil && p.Stderr != p.Stdout && p.closeFds {
		defer logger.WarnOnError(p.Stderr.Close, "Failed to close stderr")
	}

	// Start the process.
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("Unable to start process: %w", err)
	}

	p.PID = int64(cmd.Process.Pid)
	p.process = cmd.Process

	// Record the process identity so a reused PID is never signalled.
	p.BootID = bootID()
	p.StartTime, _ = procStartTime(p.PID)

	// Reset exitCode/exitErr
	p.exitCode = 0
	p.exitErr = nil

	// Spawn a goroutine waiting for it to exit.
	p.chExit = make(chan struct{})
	p.hasMonitor = true
	go func() {
		defer close(p.chExit)

		err := cmd.Wait()

		if cmd.ProcessState != nil {
			p.exitCode = int64(cmd.ProcessState.ExitCode())
		} else {
			p.exitCode = -1
		}

		if err != nil {
			p.exitErr = err

			return
		}

		if p.exitCode != 0 {
			p.exitErr = fmt.Errorf("Process exited with non-zero value %d", p.exitCode)
		}
	}()

	return nil
}

// Restart stop and starts the given process object.
func (p *Process) Restart(ctx context.Context) error {
	err := p.Stop()
	if err != nil {
		return fmt.Errorf("Unable to stop process: %w", err)
	}

	err = p.Start(ctx)
	if err != nil {
		return fmt.Errorf("Unable to start process: %w", err)
	}

	return nil
}

// Reload sends the SIGHUP signal to the given process object.
func (p *Process) Reload() error {
	pr, err := p.findProcess()
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return err
		}

		return fmt.Errorf("Could not reload process: %w", err)
	}

	err = pr.Signal(syscall.Signal(0))
	if err != nil {
		if err == os.ErrProcessDone {
			return ErrNotRunning
		}

		return fmt.Errorf("Could not reload process: %w", err)
	}

	err = pr.Signal(syscall.SIGHUP)
	if err != nil {
		return fmt.Errorf("Could not reload process: %w", err)
	}

	return nil
}

// Save will save the given process object to a YAML file. Can be imported at a later point.
func (p *Process) Save(path string) error {
	dat, err := yaml.Dump(p, yaml.WithV2Defaults())
	if err != nil {
		return fmt.Errorf("Unable to serialize process struct to YAML: %w", err)
	}

	// Write to a temporary file and rename so a partial file is never imported.
	tmpPath := path + ".tmp"
	err = os.WriteFile(tmpPath, dat, 0o644)
	if err != nil {
		return fmt.Errorf("Unable to write to file '%s': %w", tmpPath, err)
	}

	err = os.Rename(tmpPath, path)
	if err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("Unable to rename file '%s': %w", tmpPath, err)
	}

	return nil
}

// Signal will send a signal to the given process object given a signal value.
func (p *Process) Signal(signal int64) error {
	pr, err := p.findProcess()
	if err != nil {
		return err
	}

	err = pr.Signal(syscall.Signal(0))
	if err != nil {
		if err == os.ErrProcessDone {
			return ErrNotRunning
		}

		return fmt.Errorf("Could not signal process: %w", err)
	}

	err = pr.Signal(syscall.Signal(signal))
	if err != nil {
		return fmt.Errorf("Could not signal process: %w", err)
	}

	return nil
}

// Wait will wait for the given process object exit code.
func (p *Process) Wait(ctx context.Context) (int64, error) {
	if !p.hasMonitor {
		return -1, errors.New("Unable to wait on process we didn't spawn")
	}

	select {
	case <-p.chExit:
		return p.exitCode, p.exitErr
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
