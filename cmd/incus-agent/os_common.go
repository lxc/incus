//go:build darwin || windows

package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strconv"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	psUtilNet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

var (
	osShutdownSignal   = os.Interrupt
	osMetricsSupported = true
	osGuestAPISupport  = false
)

func osLoadModules() error {
	// No OS drivers to load by default.
	return nil
}

func osGetCPUMetrics(d *Daemon) ([]metrics.CPUMetrics, error) {
	cpuTimes, err := cpu.Times(true)
	if err != nil {
		return nil, err
	}

	cpuMetrics := make([]metrics.CPUMetrics, 0, len(cpuTimes))
	for _, cpuTime := range cpuTimes {
		cpuMetrics = append(cpuMetrics, metrics.CPUMetrics{
			CPU:            cpuTime.CPU,
			SecondsUser:    cpuTime.User,
			SecondsNice:    cpuTime.Nice,
			SecondsSystem:  cpuTime.System,
			SecondsIdle:    cpuTime.Idle,
			SecondsIOWait:  cpuTime.Iowait,
			SecondsIRQ:     cpuTime.Irq,
			SecondsSoftIRQ: cpuTime.Softirq,
			SecondsSteal:   cpuTime.Steal,
		})
	}

	return cpuMetrics, nil
}

func osGetDiskMetrics(d *Daemon) ([]metrics.DiskMetrics, error) {
	counters, err := disk.IOCounters()
	if err != nil {
		return nil, err
	}

	devices := make([]string, 0, len(counters))
	for device := range counters {
		devices = append(devices, device)
	}

	sort.Strings(devices)

	diskMetrics := make([]metrics.DiskMetrics, 0, len(devices))
	for _, device := range devices {
		counter := counters[device]
		diskMetrics = append(diskMetrics, metrics.DiskMetrics{
			Device:          counter.Name,
			ReadBytes:       counter.ReadBytes,
			ReadsCompleted:  counter.ReadCount,
			WrittenBytes:    counter.WriteBytes,
			WritesCompleted: counter.WriteCount,
		})
	}

	return diskMetrics, nil
}

func osGetMemoryMetrics(d *Daemon) (metrics.MemoryMetrics, error) {
	virtualMemory, err := mem.VirtualMemory()
	if err != nil {
		return metrics.MemoryMetrics{}, err
	}

	swapMemory, err := mem.SwapMemory()
	if err != nil {
		return metrics.MemoryMetrics{}, err
	}

	return metrics.MemoryMetrics{
		ActiveAnonBytes:     0,
		ActiveFileBytes:     0,
		ActiveBytes:         virtualMemory.Active,
		CachedBytes:         virtualMemory.Cached,
		DirtyBytes:          virtualMemory.Dirty,
		HugepagesFreeBytes:  virtualMemory.HugePagesFree * virtualMemory.HugePageSize,
		HugepagesTotalBytes: virtualMemory.HugePagesTotal * virtualMemory.HugePageSize,
		InactiveAnonBytes:   0,
		InactiveFileBytes:   0,
		InactiveBytes:       virtualMemory.Inactive,
		MappedBytes:         virtualMemory.Mapped,
		MemAvailableBytes:   virtualMemory.Available,
		MemFreeBytes:        virtualMemory.Free,
		MemTotalBytes:       virtualMemory.Total,
		RSSBytes:            0,
		ShmemBytes:          virtualMemory.Shared,
		SwapBytes:           swapMemory.Total,
		UnevictableBytes:    0,
		WritebackBytes:      virtualMemory.WriteBack,
		OOMKills:            0,
	}, nil
}

func osGetCPUState() api.InstanceStateCPU {
	cpuState := api.InstanceStateCPU{}

	cpuTimes, err := cpu.Times(false)
	if err != nil || len(cpuTimes) < 1 {
		cpuState.Usage = -1
	} else {
		cpuTime := cpuTimes[0]
		cpuState.Usage = int64(math.Round((cpuTime.System + cpuTime.User) * 1e9))
	}

	return cpuState
}

func osGetMemoryState() api.InstanceStateMemory {
	memory := api.InstanceStateMemory{}

	virtualMemory, err := mem.VirtualMemory()
	if err != nil {
		return memory
	}

	memory.Usage = int64(virtualMemory.Total - virtualMemory.Free)
	memory.Total = int64(virtualMemory.Total)
	return memory
}

func ipScope(ip net.IP) string {
	if ip.IsLoopback() {
		return "local"
	}

	if ip.To4() != nil {
		if ip[0] == 169 && ip[1] == 254 {
			return "link"
		}

		return "global"
	}

	if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
		return "link"
	}

	return "global"
}

func osGetNetworkState() map[string]api.InstanceStateNetwork {
	interfaces, err := psUtilNet.Interfaces()
	if err != nil {
		return map[string]api.InstanceStateNetwork{}
	}

	ioCounters, err := psUtilNet.IOCounters(true)
	if err != nil {
		return map[string]api.InstanceStateNetwork{}
	}

	// Create a map for fast lookup.
	counters := make(map[string]psUtilNet.IOCountersStat, len(ioCounters))
	for _, c := range ioCounters {
		counters[c.Name] = c
	}

	sort.Slice(interfaces, func(i, j int) bool {
		return interfaces[i].Name < interfaces[j].Name
	})

	network := make(map[string]api.InstanceStateNetwork, len(interfaces))
	for _, intf := range interfaces {
		addrs := make([]api.InstanceStateNetworkAddress, 0, len(intf.Addrs))
		for _, addr := range intf.Addrs {
			ip, ipnet, err := net.ParseCIDR(addr.Addr)
			if err != nil || ip == nil || ipnet == nil {
				continue
			}

			family := "inet"
			if ip.To4() == nil {
				family = "inet6"
			}

			ones, _ := ipnet.Mask.Size()

			addrs = append(addrs, api.InstanceStateNetworkAddress{
				Family:  family,
				Address: ip.String(),
				Netmask: strconv.Itoa(ones),
				Scope:   ipScope(ip),
			})
		}

		var cnt api.InstanceStateNetworkCounters
		counter, ok := counters[intf.Name]
		if ok {
			cnt = api.InstanceStateNetworkCounters{
				BytesReceived:          int64(counter.BytesRecv),
				BytesSent:              int64(counter.BytesSent),
				PacketsReceived:        int64(counter.PacketsRecv),
				PacketsSent:            int64(counter.PacketsSent),
				ErrorsReceived:         int64(counter.Errin),
				ErrorsSent:             int64(counter.Errout),
				PacketsDroppedOutbound: int64(counter.Dropout),
				PacketsDroppedInbound:  int64(counter.Dropin),
			}
		}

		interfaceState := "down"
		interfaceType := "unknown"
		for _, flag := range intf.Flags {
			if flag == "up" {
				interfaceState = "up"
			} else if flag == "broadcast" {
				interfaceType = "broadcast"
			} else if flag == "loopback" {
				interfaceType = "loopback"
			} else if flag == "pointtopoint" {
				interfaceType = "point-to-point"
			}
		}

		network[intf.Name] = api.InstanceStateNetwork{
			Addresses: addrs,
			Counters:  cnt,
			Hwaddr:    intf.HardwareAddr,
			HostName:  intf.Name,
			Mtu:       intf.MTU,
			State:     interfaceState,
			Type:      interfaceType,
		}
	}

	return network
}

func osGetProcessesState() int64 {
	processes, err := process.Processes()
	if err != nil {
		return -1
	}

	return int64(len(processes))
}

func osReconfigureNetworkInterfaces() {
	// Agent assisted network reconfiguration isn't currently supported.
	return
}

func osExecWrapper(ctx context.Context, pty io.ReadWriteCloser) io.ReadWriteCloser {
	return pty
}

func osGetListener(port int64) (net.Listener, error) {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on TCP: %w", err)
	}

	logger.Info("Started TCP listener")

	return l, nil
}
