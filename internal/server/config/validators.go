package config

import (
	"os/exec"
)

// AvailableExecutable checks that the given value is the name of an executable
// file, in PATH.
func AvailableExecutable(value string) error {
	if value == "none" {
		return nil
	}

	_, err := exec.LookPath(value)
	return err
}
