package fsmonitor

// FSMonitor represents a filesystem monitor.
type FSMonitor interface {
	PrefixPath() string
	Watch(path string, identifier string, f func(path string, event string) bool) error
	Unwatch(path string, identifier string) error
}
