package device

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/util"
)

// deviceJoinPath joins together prefix and text delimited by a "." for device path generation.
func deviceJoinPath(parts ...string) string {
	return strings.Join(parts, ".")
}

// validatePCIDevice returns whether a configured PCI device exists under the given address.
// It also returns nil, if an empty address is supplied.
func validatePCIDevice(address string) error {
	if address != "" && !util.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", address)) {
		return fmt.Errorf("Invalid PCI address (no device found): %s", address)
	}

	return nil
}

// checkAttachedRunningProcess checks if a device is tied to running processes.
func checkAttachedRunningProcesses(devicePath string) ([]string, error) {
	var processes []string
	procDir := "/proc"
	files, err := os.ReadDir(procDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc directory: %w", err)
	}

	for _, file := range files {
		// Check if the directory name is a number (i.e., a PID).
		_, err := strconv.Atoi(file.Name())
		if err != nil {
			continue
		}

		mapsFile := filepath.Join(procDir, file.Name(), "maps")
		f, err := os.Open(mapsFile)
		if err != nil {
			continue // If we can't read a process's maps file, skip it.
		}

		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), devicePath) {
				processes = append(processes, file.Name())
				break
			}
		}
	}

	return processes, nil
}

// getNumaNodeSet returns two slices:
// 1) the NUMA nodes parsed from the configuration,
// 2) the fallback NUMA nodes.
func getNumaNodeSet(config map[string]string) ([]int64, []int64, error) {
	// If NUMA restricted, build up a list of nodes.
	var numaNodeSet []int64
	var numaNodeSetFallback []int64

	numaNodes := config["limits.cpu.nodes"]
	if numaNodes != "" {
		if numaNodes == "balanced" {
			numaNodes = config["volatile.cpu.nodes"]
		}

		// Parse the NUMA restriction.
		numaNodeSet, err := resources.ParseNumaNodeSet(numaNodes)
		if err != nil {
			return nil, nil, err
		}

		// List all the CPUs.
		cpus, err := resources.GetCPU()
		if err != nil {
			return nil, nil, err
		}

		// Get list of socket IDs from the list of NUMA nodes.
		numaSockets := make([]uint64, 0, len(cpus.Sockets))

		for _, cpuSocket := range cpus.Sockets {
			if slices.Contains(numaSockets, cpuSocket.Socket) {
				continue
			}

			for _, cpuCore := range cpuSocket.Cores {
				found := false
				for _, cpuThread := range cpuCore.Threads {
					if slices.Contains(numaNodeSet, int64(cpuThread.NUMANode)) {
						numaSockets = append(numaSockets, cpuSocket.Socket)
						found = true
						break
					}
				}

				if found {
					break
				}
			}
		}

		// Get the list of NUMA nodes from the socket list.
		numaNodeSetFallback = []int64{}

		for _, cpuSocket := range cpus.Sockets {
			if !slices.Contains(numaSockets, cpuSocket.Socket) {
				continue
			}

			for _, cpuCore := range cpuSocket.Cores {
				for _, cpuThread := range cpuCore.Threads {
					if !slices.Contains(numaNodeSetFallback, int64(cpuThread.NUMANode)) {
						numaNodeSetFallback = append(numaNodeSetFallback, int64(cpuThread.NUMANode))
					}
				}
			}
		}
	}

	return numaNodeSet, numaNodeSetFallback, nil
}
