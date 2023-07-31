package cluster

// Option to be passed to NewGateway to customize the resulting instance.
type Option func(*options)

// LogLevel sets the logging level for messages emitted by dqlite and raft.
func LogLevel(level string) Option {
	return func(options *options) {
		options.logLevel = level
	}
}

// Latency is a coarse grain measure of how fast/reliable network links
// are. This is used to tweak the various timeouts parameters of the raft
// algorithm. See the raft.Config structure for more details. A value of 1.0
// means use the default values from hashicorp's raft package. Values closer to
// 0 reduce the values of the various timeouts (useful when running unit tests
// in-memory).
func Latency(latency float64) Option {
	return func(options *options) {
		options.latency = latency
	}
}

// Create a options instance with default values.
func newOptions() *options {
	return &options{
		latency:  1.0,
		logLevel: "ERROR",
	}
}

type options struct {
	latency  float64
	logLevel string
}
