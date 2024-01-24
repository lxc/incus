package util

import (
	"os"
	"path/filepath"
)

// CachePath returns the directory that Incus should its cache under. If INCUS_DIR is
// set, this path is $INCUS_DIR/cache, otherwise it is /var/cache/incus.
func CachePath(path ...string) string {
	varDir := os.Getenv("INCUS_DIR")
	logDir := "/var/cache/incus"
	if varDir != "" {
		logDir = filepath.Join(varDir, "cache")
	}

	items := []string{logDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// LogPath returns the directory that Incus should put logs under. If INCUS_DIR is
// set, this path is $INCUS_DIR/logs, otherwise it is /var/log/incus.
func LogPath(path ...string) string {
	varDir := os.Getenv("INCUS_DIR")
	logDir := "/var/log/incus"
	if varDir != "" {
		logDir = filepath.Join(varDir, "logs")
	}

	items := []string{logDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// RunPath returns the directory that Incus should put runtime data under.
// If INCUS_DIR is set, this path is $INCUS_DIR/run, otherwise it is /run/incus.
func RunPath(path ...string) string {
	varDir := os.Getenv("INCUS_DIR")
	runDir := "/run/incus"
	if varDir != "" {
		runDir = filepath.Join(varDir, "run")
	}

	items := []string{runDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// VarPath returns the provided path elements joined by a slash and
// appended to the end of $INCUS_DIR, which defaults to /var/lib/incus.
func VarPath(path ...string) string {
	varDir := os.Getenv("INCUS_DIR")
	if varDir == "" {
		varDir = "/var/lib/incus"
	}

	items := []string{varDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// IsDir returns true if the given path is a directory.
func IsDir(name string) bool {
	stat, err := os.Stat(name)
	if err != nil {
		return false
	}

	return stat.IsDir()
}

// IsUnixSocket returns true if the given path is either a Unix socket
// or a symbolic link pointing at a Unix socket.
func IsUnixSocket(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	return (stat.Mode() & os.ModeSocket) == os.ModeSocket
}
