package config

import (
	"os/exec"

	"github.com/sirupsen/logrus"
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

// LogLevelValidator checks whether the provided value is a valid logging level.
func LogLevelValidator(value string) error {
	if value == "" {
		return nil
	}

	_, err := logrus.ParseLevel(value)
	if err != nil {
		return err
	}

	return nil
}
