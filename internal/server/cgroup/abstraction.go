package cgroup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
)

// CGroup represents the main cgroup abstraction.
type CGroup struct {
	rw             ReadWriter
	UnifiedCapable bool
}

// SetMaxProcesses applies a limit to the number of processes.
func (cg *CGroup) SetMaxProcesses(limit int64) error {
	if !cgControllers["pids"] {
		return ErrControllerMissing
	}

	if limit == -1 {
		return cg.rw.Set("pids", "pids.max", "max")
	}

	return cg.rw.Set("pids", "pids.max", fmt.Sprintf("%d", limit))
}

// GetMemorySoftLimit returns the soft limit for memory.
func (cg *CGroup) GetMemorySoftLimit() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("memory", "memory.high")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// SetMemorySoftLimit set the soft limit for memory.
func (cg *CGroup) SetMemorySoftLimit(limit int64) error {
	if !cgControllers["memory"] {
		return ErrControllerMissing
	}

	if limit == -1 {
		return cg.rw.Set("memory", "memory.high", "max")
	}

	return cg.rw.Set("memory", "memory.high", fmt.Sprintf("%d", limit))
}

// GetMemoryLimit return the hard limit for memory.
func (cg *CGroup) GetMemoryLimit() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("memory", "memory.max")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// GetEffectiveMemoryLimit return the effective hard limit for memory.
// Returns the cgroup memory limit, or if the cgroup memory limit couldn't be determined or is larger than the
// total system memory, then the total system memory is returned.
func (cg *CGroup) GetEffectiveMemoryLimit() (int64, error) {
	memoryTotal, err := linux.DeviceTotalMemory()
	if err != nil {
		return -1, fmt.Errorf("Failed getting total memory: %q", err)
	}

	memoryLimit, err := cg.GetMemoryLimit()
	if err != nil || memoryLimit > memoryTotal {
		return memoryTotal, nil
	}

	return memoryLimit, nil
}

// SetMemoryLimit sets the hard limit for memory.
func (cg *CGroup) SetMemoryLimit(limit int64) error {
	if !cgControllers["memory"] {
		return ErrControllerMissing
	}

	if limit == -1 {
		return cg.rw.Set("memory", "memory.max", "max")
	}

	return cg.rw.Set("memory", "memory.max", fmt.Sprintf("%d", limit))
}

// GetMemoryUsage returns the current use of memory.
func (cg *CGroup) GetMemoryUsage() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("memory", "memory.current")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// GetProcessesUsage returns the current number of pids.
func (cg *CGroup) GetProcessesUsage() (int64, error) {
	if !cgControllers["pids"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("pids", "pids.current")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// SetMemorySwapLimit sets the hard limit for swap.
func (cg *CGroup) SetMemorySwapLimit(limit int64) error {
	if !cgControllers["memory"] {
		return ErrControllerMissing
	}

	if limit == -1 {
		return cg.rw.Set("memory", "memory.swap.max", "max")
	}

	return cg.rw.Set("memory", "memory.swap.max", fmt.Sprintf("%d", limit))
}

// GetCPUAcctUsageAll returns the user and system CPU times of each CPU thread in ns used by processes.
func (cg *CGroup) GetCPUAcctUsageAll() (map[int64]CPUStats, error) {
	out := map[int64]CPUStats{}

	if !cgControllers["cpu"] {
		return nil, ErrControllerMissing
	}

	val, err := cg.rw.Get("cpu", "cpu.stat")
	if err != nil {
		return nil, err
	}

	stats := CPUStats{}

	scanner := bufio.NewScanner(strings.NewReader(val))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		switch fields[0] {
		case "user_usec":
			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", val, err)
			}

			// Convert usec to nsec
			stats.User = val * 1000
		case "system_usec":
			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", val, err)
			}

			// Convert usec to nsec
			stats.System = val * 1000
		}
	}

	// Use CPU ID 0 here as cgroup v2 doesn't show the usage of separate CPUs.
	out[0] = stats

	return out, nil
}

// GetCPUAcctUsage returns the total CPU time in ns used by processes.
func (cg *CGroup) GetCPUAcctUsage() (int64, error) {
	if !cgControllers["cpu"] {
		return -1, ErrControllerMissing
	}

	stats, err := cg.rw.Get("cpu", "cpu.stat")
	if err != nil {
		return -1, err
	}

	scanner := bufio.NewScanner(strings.NewReader(stats))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		if fields[0] != "usage_usec" {
			continue
		}

		val, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		// Convert usec to nsec
		return val * 1000, nil
	}

	return -1, errors.New("Failed getting usage_usec")
}

// GetEffectiveCPUs returns the total number of effective CPUs.
func (cg *CGroup) GetEffectiveCPUs() (int, error) {
	set, err := cg.GetEffectiveCpuset()
	if err != nil {
		return -1, err
	}

	return parseCPUSet(set)
}

// parseCPUSet parses a cpuset string and returns the number of CPUs.
func parseCPUSet(set string) (int, error) {
	var out int

	fields := strings.Split(strings.TrimSpace(set), ",")
	for _, value := range fields {
		// Parse non-range values.
		if !strings.Contains(value, "-") {
			_, err := strconv.Atoi(value)
			if err != nil {
				return -1, fmt.Errorf("Failed parsing %q: %w", value, err)
			}

			out++
			continue
		}

		// Parse ranges (should be made of two elements only).
		valueFields := strings.Split(value, "-")
		if len(valueFields) != 2 {
			return -1, fmt.Errorf("Failed parsing %q: Invalid range format", value)
		}

		startRange, err := strconv.Atoi(valueFields[0])
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", valueFields[0], err)
		}

		endRange, err := strconv.Atoi(valueFields[1])
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", valueFields[1], err)
		}

		for i := startRange; i <= endRange; i++ {
			out++
		}
	}

	if out == 0 {
		return -1, fmt.Errorf("Failed parsing %q", set)
	}

	return out, nil
}

// GetMemoryMaxUsage returns the record high for memory usage.
func (cg *CGroup) GetMemoryMaxUsage() (int64, error) {
	return -1, ErrControllerMissing
}

// GetMemorySwapMaxUsage returns the record high for swap usage.
func (cg *CGroup) GetMemorySwapMaxUsage() (int64, error) {
	return -1, ErrControllerMissing
}

// SetMemorySwappiness sets swappiness paramet of vmscan.
func (cg *CGroup) SetMemorySwappiness(limit int64) error {
	return ErrControllerMissing
}

// GetMemorySwapLimit returns the hard limit on swap usage.
func (cg *CGroup) GetMemorySwapLimit() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("memory", "memory.swap.max")
	if err != nil {
		return -1, err
	}

	if val == "max" {
		return linux.GetMeminfo("SwapTotal")
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// GetMemorySwapUsage return current usage of swap.
func (cg *CGroup) GetMemorySwapUsage() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("memory", "memory.swap.current")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// GetBlkioWeight returns the currently allowed range of weights.
func (cg *CGroup) GetBlkioWeight() (int64, error) {
	if !cgControllers["io"] {
		return -1, ErrControllerMissing
	}

	val, err := cg.rw.Get("io", "io.weight")
	if err != nil {
		return -1, err
	}

	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
	}

	return n, nil
}

// SetBlkioWeight sets the currently allowed range of weights.
func (cg *CGroup) SetBlkioWeight(limit int64) error {
	if !cgControllers["io"] {
		return ErrControllerMissing
	}

	return cg.rw.Set("io", "io.weight", fmt.Sprintf("%d", limit))
}

// SetBlkioLimit sets the specified read or write limit for a device.
func (cg *CGroup) SetBlkioLimit(dev string, oType string, uType string, limit int64) error {
	if !slices.Contains([]string{"read", "write"}, oType) {
		return fmt.Errorf("Invalid I/O operation type: %s", oType)
	}

	if !slices.Contains([]string{"iops", "bps"}, uType) {
		return fmt.Errorf("Invalid I/O limit type: %s", uType)
	}

	if !cgControllers["io"] {
		return ErrControllerMissing
	}

	var op string
	switch oType {
	case "read":
		op = fmt.Sprintf("r%s", uType)
	case "write":
		op = fmt.Sprintf("w%s", uType)
	}

	return cg.rw.Set("io", "io.max", fmt.Sprintf("%s %s=%d", dev, op, limit))
}

// SetCPUShare sets the weight of each group in the same hierarchy.
func (cg *CGroup) SetCPUShare(limit int64) error {
	if !cgControllers["cpu"] {
		return ErrControllerMissing
	}

	return cg.rw.Set("cpu", "cpu.weight", fmt.Sprintf("%d", limit))
}

// SetCPUCfsLimit sets the quota and duration in ms for each scheduling period.
func (cg *CGroup) SetCPUCfsLimit(limitPeriod int64, limitQuota int64) error {
	if !cgControllers["cpu"] {
		return ErrControllerMissing
	}

	if limitPeriod == -1 && limitQuota == -1 {
		return cg.rw.Set("cpu", "cpu.max", "max")
	}

	return cg.rw.Set("cpu", "cpu.max", fmt.Sprintf("%d %d", limitQuota, limitPeriod))
}

// GetCPUCfsLimit gets the quota and duration in ms for each scheduling period.
func (cg *CGroup) GetCPUCfsLimit() (int64, int64, error) {
	if !cgControllers["cpu"] {
		return -1, -1, ErrControllerMissing
	}

	cpuMax, err := cg.rw.Get("cpu", "cpu.max")
	if err != nil {
		return -1, -1, err
	}

	cpuMaxFields := strings.Split(cpuMax, " ")
	if len(cpuMaxFields) != 2 {
		return -1, -1, errors.New("Couldn't parse CFS limits")
	}

	if cpuMaxFields[0] == "max" {
		return -1, -1, nil
	}

	limitQuota, err := strconv.ParseInt(cpuMaxFields[0], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	limitPeriod, err := strconv.ParseInt(cpuMaxFields[1], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	return limitPeriod, limitQuota, nil
}

// SetHugepagesLimit applies a limit to the number of processes.
func (cg *CGroup) SetHugepagesLimit(pageType string, limit int64) error {
	if !cgControllers["hugetlb"] {
		return ErrControllerMissing
	}

	if limit == -1 {
		// Apply the overall limit.
		err := cg.rw.Set("hugetlb", fmt.Sprintf("hugetlb.%s.max", pageType), "max")
		if err != nil {
			return err
		}

		// Apply the reserved limit.
		err = cg.rw.Set("hugetlb", fmt.Sprintf("hugetlb.%s.rsvd.max", pageType), "max")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		return nil
	}

	// Apply the overall limit.
	err := cg.rw.Set("hugetlb", fmt.Sprintf("hugetlb.%s.max", pageType), fmt.Sprintf("%d", limit))
	if err != nil {
		return err
	}

	// Apply the reserved limit.
	err = cg.rw.Set("hugetlb", fmt.Sprintf("hugetlb.%s.rsvd.max", pageType), fmt.Sprintf("%d", limit))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// GetEffectiveCpuset returns the current set of CPUs for the cgroup.
func (cg *CGroup) GetEffectiveCpuset() (string, error) {
	if !cgControllers["cpuset"] {
		return "", ErrControllerMissing
	}

	return cg.rw.Get("cpuset", "cpuset.cpus.effective")
}

// GetCpuset returns the current set of CPUs for the cgroup.
func (cg *CGroup) GetCpuset() (string, error) {
	if !cgControllers["cpuset"] {
		return "", ErrControllerMissing
	}

	return cg.rw.Get("cpuset", "cpuset.cpus")
}

// SetCpuset set the currently allowed set of CPUs for the cgroups.
func (cg *CGroup) SetCpuset(limit string) error {
	if !cgControllers["cpuset"] {
		return ErrControllerMissing
	}

	return cg.rw.Set("cpuset", "cpuset.cpus", limit)
}

// GetMemoryStats returns memory stats.
func (cg *CGroup) GetMemoryStats() (map[string]uint64, error) {
	out := make(map[string]uint64)

	if !cgControllers["memory"] {
		return nil, ErrControllerMissing
	}

	stats, err := cg.rw.Get("memory", "memory.stat")
	if err != nil {
		return nil, err
	}

	for _, stat := range strings.Split(stats, "\n") {
		field := strings.Split(stat, " ")

		switch field[0] {
		case "total_active_anon", "active_anon":
			out["active_anon"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_active_file", "active_file":
			out["active_file"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_inactive_anon", "inactive_anon":
			out["inactive_anon"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_inactive_file", "inactive_file":
			out["inactive_file"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_unevictable", "unevictable":
			out["unevictable"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_writeback", "file_writeback":
			out["writeback"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_dirty", "file_dirty":
			out["dirty"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_mapped_file", "file_mapped":
			out["mapped"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_rss": // v1 only
			out["rss"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_shmem", "shmem":
			out["shmem"], _ = strconv.ParseUint(field[1], 10, 64)
		case "total_cache", "file":
			out["cache"], _ = strconv.ParseUint(field[1], 10, 64)
		}
	}

	// Calculated values
	out["active"] = out["active_anon"] + out["active_file"]
	out["inactive"] = out["inactive_anon"] + out["inactive_file"]

	return out, nil
}

// GetOOMKills returns the number of oom kills.
func (cg *CGroup) GetOOMKills() (int64, error) {
	if !cgControllers["memory"] {
		return -1, ErrControllerMissing
	}

	stats, err := cg.rw.Get("memory", "memory.events")
	if err != nil {
		return -1, err
	}

	for _, stat := range strings.Split(stats, "\n") {
		field := strings.Split(stat, " ")
		// skip incorrect lines
		if len(field) != 2 {
			continue
		}

		switch field[0] {
		case "oom_kill":
			out, _ := strconv.ParseInt(field[1], 10, 64)

			return out, nil
		}
	}

	return -1, errors.New("Failed getting oom_kill")
}

// GetIOStats returns disk stats.
func (cg *CGroup) GetIOStats() (map[string]*IOStats, error) {
	partitions, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/partitions: %w", err)
	}

	// partMap maps major:minor to device names, e.g. 259:0 -> nvme0n1
	partMap := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(partitions))

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		// Ignore the header
		if fields[0] == "major" {
			continue
		}

		partMap[fmt.Sprintf("%s:%s", fields[0], fields[1])] = fields[3]
	}

	// ioMap contains io stats for each device
	ioMap := make(map[string]*IOStats)

	if !cgControllers["io"] {
		return nil, ErrControllerMissing
	}

	val, err := cg.rw.Get("io", "io.stat")
	if err != nil {
		return nil, fmt.Errorf("Failed getting io.stat: %w", err)
	}

	scanner = bufio.NewScanner(strings.NewReader(val))

	for scanner.Scan() {
		var devID string
		ioStats := &IOStats{}

		for _, statPart := range strings.Split(scanner.Text(), " ") {
			// If the stat part is empty, skip it.
			if statPart == "" {
				continue
			}

			// Skip unknown devices.
			if statPart == "(unknown)" {
				devID = ""
				continue
			}

			if strings.Contains(statPart, ":") {
				// Store the last dev ID as this works around a kernel bug where multiple dev IDs could appear on a single line.
				devID = statPart
				continue
			}

			// Skip loop devices (major dev ID 7) as they are irrelevant.
			if strings.HasPrefix(devID, "7:") {
				continue
			}

			// Parse the stat value.
			statName, statValueStr, found := strings.Cut(statPart, "=")
			if !found {
				return nil, fmt.Errorf("Failed extracting io.stat %q (from %q)", statPart, scanner.Text())
			}

			statValue, err := strconv.ParseUint(statValueStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing io.stat %q %q (from %q): %w", statName, statValueStr, scanner.Text(), err)
			}

			switch statName {
			case "rbytes":
				ioStats.ReadBytes = statValue
			case "wbytes":
				ioStats.WrittenBytes = statValue
			case "rios":
				ioStats.ReadsCompleted = statValue
			case "wios":
				ioStats.WritesCompleted = statValue
			}
		}

		ioMap[partMap[devID]] = ioStats
	}

	return ioMap, nil
}
