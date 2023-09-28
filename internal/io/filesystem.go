package io

import (
	"os"
)

// GetPathMode returns a os.FileMode for the provided path.
func GetPathMode(path string) (os.FileMode, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return os.FileMode(0000), err
	}

	mode, _, _ := GetOwnerMode(fi)
	return mode, nil
}
