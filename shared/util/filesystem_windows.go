//go:build windows

package util

func PathIsWritable(path string) bool {
	return true
}
