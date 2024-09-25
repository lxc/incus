package resources

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/units"
)

var sysDevicesNode = "/sys/devices/system/node"
var sysDevicesSystemMemory = "/sys/devices/system/memory"

type meminfo struct {
	Cached         uint64
	Buffers        uint64
	Total          uint64
	Free           uint64
	Used           uint64
	HugepagesTotal uint64
	HugepagesFree  uint64
	HugepagesSize  uint64
}

func parseMeminfo(path string) (*meminfo, error) {
	memory := meminfo{}

	// Open meminfo
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %q: %w", path, err)
	}

	defer func() { _ = f.Close() }()
	memInfo := bufio.NewScanner(f)

	// Get common memory information
	for memInfo.Scan() {
		line := strings.TrimSpace(memInfo.Text())

		// Get key/value
		fields := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(fields[0])
		keyFields := strings.Split(key, " ")
		key = keyFields[len(keyFields)-1]
		value := strings.TrimSpace(fields[1])
		value = strings.Replace(value, " kB", "KiB", 1)

		if key == "MemTotal" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse MemTotal: %w", err)
			}

			memory.Total = uint64(bytes)
			continue
		}

		if key == "MemFree" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse MemFree: %w", err)
			}

			memory.Free = uint64(bytes)
			continue
		}

		if key == "MemUsed" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse MemUsed: %w", err)
			}

			memory.Used = uint64(bytes)
			continue
		}

		if key == "Cached" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse Cached: %w", err)
			}

			memory.Cached = uint64(bytes)
			continue
		}

		if key == "Buffers" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse Buffers: %w", err)
			}

			memory.Buffers = uint64(bytes)
			continue
		}

		if key == "HugePages_Total" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse HugePages_Total: %w", err)
			}

			memory.HugepagesTotal = uint64(bytes)
			continue
		}

		if key == "HugePages_Free" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse HugePages_Free: %w", err)
			}

			memory.HugepagesFree = uint64(bytes)
			continue
		}

		if key == "Hugepagesize" {
			bytes, err := units.ParseByteSizeString(value)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse Hugepagesize: %w", err)
			}

			memory.HugepagesSize = uint64(bytes)
			continue
		}
	}

	return &memory, nil
}

func getMemoryBlockSizeBytes() uint64 {
	memoryBlockSizePath := filepath.Join(sysDevicesSystemMemory, "block_size_bytes")

	if !sysfsExists(memoryBlockSizePath) {
		return 0
	}

	// Get block size
	content, err := os.ReadFile(memoryBlockSizePath)
	if err != nil {
		return 0
	}

	blockSize, err := strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
	if err != nil {
		return 0
	}

	return blockSize
}

func getTotalMemory(sysDevicesBase string) uint64 {
	blockSize := getMemoryBlockSizeBytes()
	if blockSize == 0 {
		return 0
	}

	entries, err := os.ReadDir(sysDevicesBase)
	if err != nil {
		return 0
	}

	// Count the number of blocks
	var count uint64
	for _, entry := range entries {
		entryName := entry.Name()
		entryPath := filepath.Join(sysDevicesBase, entryName)

		// Ignore directories not starting with "memory"
		if !strings.HasPrefix(entryName, "memory") {
			continue
		}

		// Ignore invalid entries.
		if !sysfsExists(filepath.Join(entryPath, "online")) {
			continue
		}

		content, err := os.ReadFile(filepath.Join(entryPath, "online"))
		if err != nil {
			return 0
		}

		// Only count the block if it's online
		if strings.TrimSpace(string(content)) == "1" {
			count++
		}
	}

	return blockSize * count
}

// GetMemory returns a filled api.ResourcesMemory struct ready for use by Incus.
func GetMemory() (*api.ResourcesMemory, error) {
	memory := api.ResourcesMemory{}

	// Parse main meminfo
	info, err := parseMeminfo("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("Failed to parse /proc/meminfo: %w", err)
	}

	// Calculate the total memory from /sys/devices/system/memory, as the previously determined
	// value reports the amount of available system memory minus the amount reserved for the kernel.
	// If successful, replace the previous value, retrieved from /proc/meminfo.
	memTotal := getTotalMemory(sysDevicesSystemMemory)
	if memTotal > 0 {
		info.Total = memTotal
	}

	// Fill used values
	memory.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * info.HugepagesSize
	memory.HugepagesTotal = info.HugepagesTotal * info.HugepagesSize
	memory.HugepagesSize = info.HugepagesSize

	memory.Used = info.Total - info.Free - info.Cached - info.Buffers
	memory.Total = info.Total

	// Get NUMA information
	if sysfsExists(sysDevicesNode) {
		memory.Nodes = []api.ResourcesMemoryNode{}

		// List all the nodes
		entries, err := os.ReadDir(sysDevicesNode)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysDevicesNode, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysDevicesNode, entryName)

			if !sysfsExists(filepath.Join(entryPath, "meminfo")) {
				continue
			}

			// Get NUMA node number
			nodeName := strings.TrimPrefix(entryName, "node")
			nodeNumber, err := strconv.ParseUint(nodeName, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to find NUMA node: %w", err)
			}

			// Parse NUMA meminfo
			info, err := parseMeminfo(filepath.Join(entryPath, "meminfo"))
			if err != nil {
				return nil, fmt.Errorf("Failed to parse %q: %w", filepath.Join(entryPath, "meminfo"), err)
			}

			// Setup the entry
			node := api.ResourcesMemoryNode{}
			node.NUMANode = nodeNumber

			node.HugepagesUsed = (info.HugepagesTotal - info.HugepagesFree) * memory.HugepagesSize
			node.HugepagesTotal = info.HugepagesTotal * memory.HugepagesSize

			node.Used = info.Used
			node.Total = info.Total

			// Calculate the total memory from /sys/devices/system/node/memory, as the previously determined
			// value reports the amount of available system memory minus the amount reserved for the kernel.
			// If successful, replace the previous value, retrieved from /sys/devices/system/node/meminfo.
			memTotal := getTotalMemory(entryPath)
			if memTotal > 0 {
				node.Total = memTotal
			}

			memory.Nodes = append(memory.Nodes, node)
		}
	}

	return &memory, nil
}
