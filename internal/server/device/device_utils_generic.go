package device

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
