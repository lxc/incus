//go:build linux && cgo && !agent

package util

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/osarch"
)

// GetArchitectures returns the list of supported architectures.
func GetArchitectures() ([]int, error) {
	architectures := []int{}

	architectureName, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return nil, err
	}

	architecture, err := osarch.ArchitectureId(architectureName)
	if err != nil {
		return nil, err
	}

	architectures = append(architectures, architecture)

	personalities, err := osarch.ArchitecturePersonalities(architecture)
	if err != nil {
		return nil, err
	}

	architectures = append(architectures, personalities...)

	return architectures, nil
}

// GetExecPath returns the path to the current binary.
func GetExecPath() string {
	execPath := os.Getenv("INCUS_EXEC_PATH")
	if execPath != "" {
		return execPath
	}

	execPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		execPath = "bad-exec-path"
	}

	// The execPath from /proc/self/exe can end with " (deleted)" if the binary has been removed/changed
	// since it was first started, strip this so that we only return a valid path.
	return strings.TrimSuffix(execPath, " (deleted)")
}

// ReplaceDaemon replaces the daemon by re-execing the binary.
func ReplaceDaemon() error {
	err := unix.Exec(GetExecPath(), os.Args, os.Environ())
	if err != nil {
		return err
	}

	return nil
}
