package util

import (
	"errors"
	"io/fs"
	"os"
)

func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return false
	}

	return true
}
