//go:build windows

package subprocess

import (
	"context"
	"fmt"
	"os"
)

// Process struct. Has ability to set runtime arguments.
type Process struct{}

// GetPid returns the pid for the given process object.
func (p *Process) GetPid() (int64, error) {
	return -1, fmt.Errorf("Windows isn't supported at this time")
}

// SetApparmor allows setting the AppArmor profile.
func (p *Process) SetApparmor(profile string) {}

// SetCreds allows setting process credentials.
func (p *Process) SetCreds(uid uint32, gid uint32) {}

// Stop will stop the given process object.
func (p *Process) Stop() error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Start will start the given process object.
func (p *Process) Start(ctx context.Context) error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// StartWithFiles will start the given process object with extra file descriptors.
func (p *Process) StartWithFiles(ctx context.Context, fds []*os.File) error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Restart stop and starts the given process object.
func (p *Process) Restart(ctx context.Context) error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Reload sends the SIGHUP signal to the given process object.
func (p *Process) Reload() error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Save will save the given process object to a YAML file. Can be imported at a later point.
func (p *Process) Save(path string) error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Signal will send a signal to the given process object given a signal value.
func (p *Process) Signal(signal int64) error {
	return fmt.Errorf("Windows isn't supported at this time")
}

// Wait will wait for the given process object exit code.
func (p *Process) Wait(ctx context.Context) (int64, error) {
	return -1, fmt.Errorf("Windows isn't supported at this time")
}
