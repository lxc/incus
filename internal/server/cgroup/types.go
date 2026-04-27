package cgroup

var cgPath = "/sys/fs/cgroup"

// The ReadWriter interface is used to read/write cgroup data.
type ReadWriter interface {
	Get(controller string, key string) (string, error)
	Set(controller string, key string, value string) error
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
