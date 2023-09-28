//go:build windows

package io

import (
	"os"
)

func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	return fInfo.Mode(), -1, -1
}
