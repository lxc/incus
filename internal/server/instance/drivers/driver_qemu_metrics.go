package drivers

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

func (d *qemu) getQemuMetrics() (*metrics.MetricSet, error) {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
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

	networkState, err := d.getNetworkState()
	if err != nil {
		d.logger.Warn("Failed to get network metrics", logger.Ctx{"err": err})
	} else {
		out.Network = make([]metrics.NetworkMetrics, 0, len(networkState))

		for name, state := range networkState {
			out.Network = append(out.Network, metrics.NetworkMetrics{
				Device:          name,
				ReceiveBytes:    uint64(state.Counters.BytesReceived),
				ReceiveDrop:     uint64(state.Counters.PacketsDroppedInbound),
				ReceiveErrors:   uint64(state.Counters.ErrorsReceived),
				ReceivePackets:  uint64(state.Counters.PacketsReceived),
				TransmitBytes:   uint64(state.Counters.BytesSent),
				TransmitDrop:    uint64(state.Counters.PacketsDroppedOutbound),
				TransmitErrors:  uint64(state.Counters.ErrorsSent),
				TransmitPackets: uint64(state.Counters.PacketsSent),
			})
		}
	}

	metricSet, err := metrics.MetricSetFromAPI(&out, map[string]string{"project": d.project.Name, "name": d.name, "type": instancetype.VM.String()})
	if err != nil {
		return nil, err
	}

	return metricSet, nil
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

	defer func() { _ = f.Close() }()

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
		value := strings.Replace(fields[len(fields)-1], " ", "", -1)

		// Feed the result to units.ParseByteSizeString to get an int value
		valueBytes, err := units.ParseByteSizeString(value)
		if err != nil {
			return out, err
		}

		memRSS = valueBytes
		break
	}

	if memRSS == -1 {
		return out, fmt.Errorf("Couldn't find VM memory usage")
	}

	// Get max memory usage.
	memTotal := d.expandedConfig["limits.memory"]
	if memTotal == "" {
		memTotal = QEMUDefaultMemSize // Default if no memory limit specified.
	}

	memTotalBytes, err := units.ParseByteSizeString(memTotal)
	if err != nil {
		return out, err
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

	cpuMetrics := make([]metrics.CPUMetrics, 0, len(threadIDs))

	for i, threadID := range threadIDs {
		pid, err := os.ReadFile(d.pidFilePath())
		if err != nil {
			return nil, err
		}

		statFile := filepath.Join("/proc", strings.TrimSpace(string(pid)), "task", strconv.Itoa(threadID), "stat")

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
