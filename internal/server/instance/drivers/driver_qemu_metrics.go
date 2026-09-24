package drivers

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/internal/server/instance/drivers/qemudefault"
	"github.com/lxc/incus/v7/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/metrics"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/resources"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

func (d *qemu) getQemuMetrics() (*metrics.MetricSet, error) {
	// Connect to the monitor.
	monitor, err := d.qmpConnect()
	if err != nil {
		return nil, err
	}

	out := metrics.Metrics{}

	cpuStats, err := d.getQemuCPUMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get CPU metrics", logger.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	memoryStats, err := d.getQemuMemoryMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get memory metrics", logger.Ctx{"err": err})
	} else {
		out.Memory = memoryStats
	}

	diskStats, err := d.getQemuDiskMetrics(monitor)
	if err != nil {
		d.logger.Warn("Failed to get disk metrics", logger.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	networkStats, err := d.getQemuNetworkMetrics()
	if err != nil {
		d.logger.Warn("Failed to get network metrics", logger.Ctx{"err": err})
	} else {
		out.Network = networkStats
	}

	metricSet, err := metrics.MetricSetFromAPI(&out, map[string]string{"project": d.project.Name, "name": d.name, "type": instancetype.VM.String()})
	if err != nil {
		return nil, err
	}

	return metricSet, nil
}

// getQemuNetworkMetrics reads the host side counters of every NIC without instantiating the devices.
func (d *qemu) getQemuNetworkMetrics() ([]metrics.NetworkMetrics, error) {
	out := []metrics.NetworkMetrics{}

	for name, config := range d.ExpandedDevices() {
		if config["type"] != "nic" || config["nested"] != "" {
			continue
		}

		hostName := d.localConfig[fmt.Sprintf("volatile.%s.host_name", name)]
		if hostName == "" || !network.InterfaceExists(hostName) {
			continue
		}

		// The counters are reported from the instance's point of view, so reverse the host ones.
		hostCounters, err := resources.GetNetworkCounters(hostName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting network interface counters for %q: %w", name, err)
		}

		out = append(out, metrics.NetworkMetrics{
			Device:          name,
			ReceiveBytes:    uint64(hostCounters.BytesSent),
			ReceivePackets:  uint64(hostCounters.PacketsSent),
			TransmitBytes:   uint64(hostCounters.BytesReceived),
			TransmitPackets: uint64(hostCounters.PacketsReceived),
		})
	}

	return out, nil
}

func (d *qemu) getQemuDiskMetrics(monitor *qmp.Monitor) ([]metrics.DiskMetrics, error) {
	stats, err := monitor.GetBlockStats()
	if err != nil {
		return nil, err
	}

	out := make([]metrics.DiskMetrics, 0, len(stats))

	for dev, stat := range stats {
		out = append(out, metrics.DiskMetrics{
			Device:          dev,
			ReadBytes:       uint64(stat.BytesRead),
			ReadsCompleted:  uint64(stat.ReadsCompleted),
			WrittenBytes:    uint64(stat.BytesWritten),
			WritesCompleted: uint64(stat.WritesCompleted),
		})
	}

	return out, nil
}

func (d *qemu) getQemuMemoryMetrics(monitor *qmp.Monitor) (metrics.MemoryMetrics, error) {
	out := metrics.MemoryMetrics{}

	// Get the QEMU PID.
	pid, err := d.pid()
	if err != nil {
		return out, err
	}

	// Extract current QEMU RSS.
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return out, err
	}

	defer logger.WarnOnError(f.Close, "Failed to close file")

	// Read it line by line.
	memRSS := int64(-1)

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about VmRSS.
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}

		// Extract the before last (value) and last (unit) fields
		fields := strings.Split(line, "\t")
		value := strings.ReplaceAll(fields[len(fields)-1], " ", "")

		// Feed the result to units.ParseByteSizeString to get an int value
		valueBytes, err := units.ParseByteSizeString(value)
		if err != nil {
			return out, err
		}

		memRSS = valueBytes
		break
	}

	if memRSS == -1 {
		return out, errors.New("Couldn't find VM memory usage")
	}

	// Get max memory usage.
	memTotal := d.expandedConfig["limits.memory"]
	if memTotal == "" {
		memTotal = qemudefault.MemSize // Default if no memory limit specified.
	}

	memTotalBytes, err := units.ParseByteSizeString(memTotal)
	if err != nil {
		return out, err
	}

	// Handle host usage being larger than limit.
	if memRSS > memTotalBytes {
		memRSS = memTotalBytes
	}

	// Prepare struct.
	out = metrics.MemoryMetrics{
		MemAvailableBytes: uint64(memTotalBytes - memRSS),
		MemFreeBytes:      uint64(memTotalBytes - memRSS),
		MemTotalBytes:     uint64(memTotalBytes),
	}

	return out, nil
}

func (d *qemu) getQemuCPUMetrics(monitor *qmp.Monitor) ([]metrics.CPUMetrics, error) {
	// Get CPU metrics
	threadIDs, err := monitor.GetCPUs()
	if err != nil {
		return nil, err
	}

	pid, err := os.ReadFile(d.pidFilePath())
	if err != nil {
		return nil, err
	}

	taskPath := filepath.Join("/proc", strings.TrimSpace(string(pid)), "task")
	cpuMetrics := make([]metrics.CPUMetrics, 0, len(threadIDs))

	for i, threadID := range threadIDs {
		statFile := filepath.Join(taskPath, strconv.Itoa(threadID), "stat")

		if !util.PathExists(statFile) {
			continue
		}

		content, err := os.ReadFile(statFile)
		if err != nil {
			return nil, err
		}

		fields := strings.Fields(string(content))

		stats := metrics.CPUMetrics{}

		stats.SecondsUser, err = strconv.ParseFloat(fields[13], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[13], err)
		}

		guestTime, err := strconv.ParseFloat(fields[42], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[42], err)
		}

		// According to proc(5), utime includes guest_time which therefore needs to be subtracted to get the correct time.
		stats.SecondsUser -= guestTime
		stats.SecondsUser /= 100

		stats.SecondsSystem, err = strconv.ParseFloat(fields[14], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[14], err)
		}

		stats.SecondsSystem /= 100

		stats.CPU = fmt.Sprintf("cpu%d", i)

		cpuMetrics = append(cpuMetrics, stats)
	}

	return cpuMetrics, nil
}
