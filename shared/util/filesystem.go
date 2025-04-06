package util

import (
	"errors"
	"io/fs"
	"os"
)

// PathExists checks if the provided path exists.
func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return false
	}

	return true
}
