//go:build windows

package subprocess

import (
	"errors"
	"io"
)

// NewProcess is a constructor for a process object. Represents a process with argument config.
// stdoutPath and stderrPath arguments are optional. Returns an address to process.
func NewProcess(name string, args []string, stdoutPath string, stderrPath string) (*Process, error) {
	return nil, errors.New("Windows isn't supported at this time")
}

// NewProcessWithFds is a constructor for a process object. Represents a process with argument config. Returns an address to process.
func NewProcessWithFds(name string, args []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.WriteCloser) *Process {
	return nil
}

// ImportProcess imports a saved process into a subprocess object.
func ImportProcess(path string) (*Process, error) {
	return nil, errors.New("Windows isn't supported at this time")
}
