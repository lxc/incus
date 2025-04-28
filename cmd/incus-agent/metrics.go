package main

import (
	"net/http"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/logger"
)

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet},
}

func metricsGet(d *Daemon, r *http.Request) response.Response {
	if !osMetricsSupported {
		return response.NotFound(nil)
	}

	out := metrics.Metrics{}

	diskStats, err := osGetDiskMetrics(d)
	if err != nil {
		logger.Warn("Failed to get disk metrics", logger.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	filesystemStats, err := osGetFilesystemMetrics(d)
	if err != nil {
		logger.Warn("Failed to get filesystem metrics", logger.Ctx{"err": err})
	} else {
		out.Filesystem = filesystemStats
	}

	memStats, err := osGetMemoryMetrics(d)
	if err != nil {
		logger.Warn("Failed to get memory metrics", logger.Ctx{"err": err})
	} else {
		out.Memory = memStats
	}

	netStats, err := getNetworkMetrics(d)
	if err != nil {
		logger.Warn("Failed to get network metrics", logger.Ctx{"err": err})
	} else {
		out.Network = netStats
	}

	out.ProcessesTotal = uint64(osGetProcessesState())

	cpuStats, err := osGetCPUMetrics(d)
	if err != nil {
		logger.Warn("Failed to get CPU metrics", logger.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	return response.SyncResponse(true, &out)
}

func getNetworkMetrics(d *Daemon) ([]metrics.NetworkMetrics, error) {
	out := []metrics.NetworkMetrics{}

	for dev, state := range osGetNetworkState() {
		stats := metrics.NetworkMetrics{}

		stats.ReceiveBytes = uint64(state.Counters.BytesReceived)
		stats.ReceiveDrop = uint64(state.Counters.PacketsDroppedInbound)
		stats.ReceiveErrors = uint64(state.Counters.ErrorsReceived)
		stats.ReceivePackets = uint64(state.Counters.PacketsReceived)
		stats.TransmitBytes = uint64(state.Counters.BytesSent)
		stats.TransmitDrop = uint64(state.Counters.PacketsDroppedOutbound)
		stats.TransmitErrors = uint64(state.Counters.ErrorsSent)
		stats.TransmitPackets = uint64(state.Counters.PacketsSent)

		stats.Device = dev

		out = append(out, stats)
	}

	return out, nil
}
