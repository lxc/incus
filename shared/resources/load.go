package resources

import (
	"os"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
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

	loadAvgsBuf, err := os.ReadFile("/proc/loadavg")
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
	dirEntries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}

	total := 0
	for _, entry := range dirEntries {
		if entry.IsDir() {
			_, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			} else {
				total += 1
			}
		}
	}

	return total, nil
}
