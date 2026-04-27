package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewFileReadWriter returns a CGroup instance using the filesystem as its backend.
func NewFileReadWriter(pid int) (*CGroup, error) {
	// Setup the read/writer struct.
	rw := fileReadWriter{}

	// Get the cgroup paths.
	controllers, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(string(controllers), "\n") {
		// Skip empty lines.
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Extract the fields.
		fields := strings.Split(line, ":")

		// Check for the unified cgroup.
		if fields[0] != "0" {
			continue
		}

		path := filepath.Join("/sys/fs/cgroup", fields[2])

		if strings.HasSuffix(fields[2], "/init.scope") {
			path = filepath.Dir(path)
		}

		rw.path = path
		break
	}

	cg, err := New(&rw)
	if err != nil {
		return nil, err
	}

	return cg, nil
}

type fileReadWriter struct {
	path string
}

// Get reads the value of a cgroup key.
func (rw *fileReadWriter) Get(controller string, key string) (string, error) {
	path := filepath.Join(rw.path, key)

	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(value)), nil
}

// Set writes a value to a cgroup key.
func (rw *fileReadWriter) Set(controller string, key string, value string) error {
	path := filepath.Join(rw.path, key)

	return os.WriteFile(path, []byte(value), 0o600)
}
