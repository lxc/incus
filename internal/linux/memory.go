package linux

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/units"
)

// DeviceTotalMemory returns the total amount of memory on the system (in bytes).
func DeviceTotalMemory() (int64, error) {
	return GetMeminfo("MemTotal")
}

// GetMeminfo parses /proc/meminfo for the specified field.
func GetMeminfo(field string) (int64, error) {
	// Open /proc/meminfo
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1, err
	}

	defer func() { _ = f.Close() }()

	// Read it line by line
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about MemTotal
		if !strings.HasPrefix(line, field+":") {
			continue
		}

		// Extract the before last (value) and last (unit) fields
		fields := strings.Split(line, " ")
		value := fields[len(fields)-2] + fields[len(fields)-1]

		// Feed the result to units.ParseByteSizeString to get an int value
		valueBytes, err := units.ParseByteSizeString(value)
		if err != nil {
			return -1, err
		}

		return valueBytes, nil
	}

	return -1, fmt.Errorf("Couldn't find %s", field)
}
