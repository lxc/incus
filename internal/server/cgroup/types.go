package cgroup

var cgPath = "/sys/fs/cgroup"

// Backend indicates whether to use v1, v2 or unavailable.
type Backend int

const (
	// Unavailable indicates the lack of controller.
	Unavailable = Backend(0)

	// V1 indicates the controller is backed by Cgroup V1.
	V1 = Backend(1)

	// V2 indicates the controller is backed by Cgroup V2.
	V2 = Backend(2)
)

// The ReadWriter interface is used to read/write cgroup data.
type ReadWriter interface {
	Get(backend Backend, controller string, key string) (string, error)
	Set(backend Backend, controller string, key string, value string) error
}

// IOStats represent IO stats.
type IOStats struct {
	ReadBytes       uint64
	ReadsCompleted  uint64
	WrittenBytes    uint64
	WritesCompleted uint64
}

// CPUStats represent CPU stats.
type CPUStats struct {
	User   int64
	System int64
}
