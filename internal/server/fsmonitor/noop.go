package fsmonitor

import (
	"errors"
)

// noopMonitor stands in when no filesystem monitor could be started.
type noopMonitor struct {
	prefixPath string
}

// NewNoop returns a monitor that refuses every watch.
func NewNoop(prefixPath string) FSMonitor {
	return &noopMonitor{prefixPath: prefixPath}
}

// PrefixPath returns the prefix path.
func (fs *noopMonitor) PrefixPath() string {
	return fs.prefixPath
}

// Watch always fails as no filesystem monitor is available.
func (fs *noopMonitor) Watch(path string, identifier string, f func(path string, event string) bool) error {
	return errors.New("No filesystem monitor is available")
}

// Unwatch does nothing as nothing is ever watched.
func (fs *noopMonitor) Unwatch(path string, identifier string) error {
	return nil
}
