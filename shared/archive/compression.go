package archive

import (
	"runtime"
)

// CompressionThreads returns the optimal number of threads for parallel (de)compression.
// The value is kept low to avoid load spikes affecting the running instances.
func CompressionThreads() int {
	cpus := runtime.NumCPU()

	if cpus >= 16 {
		return 4
	}

	if cpus >= 4 {
		return 2
	}

	return 1
}
