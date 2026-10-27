//go:build linux

package resources

import (
	"os"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/shared/api"
)

// GetLoad returns the system load information.
func GetLoad() (*api.ResourcesLoad, error) {
	loadAvgs, err := getLoadAvgs()
	if err != nil {
		return nil, err
	}

	processes, err := getProcessCount()
	if err != nil {
		return nil, err
	}

	loadAverage := api.ResourcesLoad{
		Average1Min:  loadAvgs[0],
		Average5Min:  loadAvgs[1],
		Average10Min: loadAvgs[2],
		Processes:    processes,
	}

	return &loadAverage, nil
}

// getLoadAvgs returns the host's load averages from /proc/loadavg.
func getLoadAvgs() ([]float64, error) {
	loadAvgs := make([]float64, 3)

	loadAvgsBuf, err := readKernelFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}

	loadAvgFields := strings.Fields(string(loadAvgsBuf))

	loadAvgs[0], err = strconv.ParseFloat(loadAvgFields[0], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[1], err = strconv.ParseFloat(loadAvgFields[1], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[2], err = strconv.ParseFloat(loadAvgFields[2], 64)
	if err != nil {
		return nil, err
	}

	return loadAvgs, nil
}

// getProcessCount returns the count of all processes on the system.
func getProcessCount() (int, error) {
	f, err := os.Open("/proc")
	if err != nil {
		return 0, err
	}

	defer func() { _ = f.Close() }()

	// Only read the names, the numeric entries are the processes.
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, name := range names {
		if name[0] < '0' || name[0] > '9' {
			continue
		}

		_, err := strconv.Atoi(name)
		if err != nil {
			continue
		}

		total++
	}

	return total, nil
}
