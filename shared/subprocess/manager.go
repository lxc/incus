//go:build !windows

package subprocess

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

// NewProcess is a constructor for a process object. Represents a process with argument config.
// stdoutPath and stderrPath arguments are optional. Returns an address to process.
func NewProcess(name string, args []string, stdoutPath string, stderrPath string) (*Process, error) {
	var stdout, stderr io.WriteCloser
	var err error
	// Setup output capture.
	if stdoutPath != "" {
		stdout, err = os.Create(stdoutPath)
		if err != nil {
			return nil, fmt.Errorf("Unable to open stdout file %q: %w", stdoutPath, err)
		}
	}
	if stderrPath == stdoutPath {
		stderr = stdout
	} else if stderrPath != "" {
		stderr, err = os.Create(stderrPath)
		if err != nil {
			return nil, fmt.Errorf("Unable to open stderr file %q: %w", stderrPath, err)
		}
	}

	p := NewProcessWithFds(name, args, nil, stdout, stderr)
	p.closeFds = true

	return p, nil
}

// NewProcessWithFds is a constructor for a process object. Represents a process with argument config. Returns an address to process.
func NewProcessWithFds(name string, args []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.WriteCloser) *Process {
	proc := Process{
		Name:   name,
		Args:   args,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	return &proc
}

// ImportProcess imports a saved process into a subprocess object.
func ImportProcess(path string) (*Process, error) {
	dat, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to read PID file %q: %w", path, err)
	}

	proc := Process{}
	err = yaml.Unmarshal(dat, &proc)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse YAML in PID file %q: %w", path, err)
	}

	return &proc, nil
}
