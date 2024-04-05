package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var stateCmd = APIEndpoint{
	Name: "state",
	Path: "state",

	Get: APIEndpointAction{Handler: stateGet},
	Put: APIEndpointAction{Handler: statePut},
}

func stateGet(d *Daemon, r *http.Request) response.Response {
	return response.SyncResponse(true, renderState())
}

func statePut(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

func renderState() *api.InstanceState {
	return &api.InstanceState{
		CPU:       cpuState(),
		Memory:    memoryState(),
		Network:   networkState(),
		Pid:       1,
		Processes: processesState(),
	}
}

func cpuState() api.InstanceStateCPU {
	var value []byte
	var err error
	cpu := api.InstanceStateCPU{}

	if util.PathExists("/sys/fs/cgroup/cpuacct/cpuacct.usage") {
		// CPU usage in seconds
		value, err = os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage")
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		valueInt, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		cpu.Usage = valueInt

		return cpu
	} else if util.PathExists("/sys/fs/cgroup/cpu.stat") {
		stats, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		scanner := bufio.NewScanner(bytes.NewReader(stats))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if fields[0] == "usage_usec" {
				valueInt, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					cpu.Usage = -1
					return cpu
				}

				// usec -> nsec
				cpu.Usage = valueInt * 1000
				return cpu
			}
		}
	}

	cpu.Usage = -1
	return cpu
}

func memoryState() api.InstanceStateMemory {
	memory := api.InstanceStateMemory{}

	stats, err := getMemoryMetrics(nil)
	if err != nil {
		return memory
	}

	memory.Usage = int64(stats.MemTotalBytes) - int64(stats.MemFreeBytes)
	memory.Total = int64(stats.MemTotalBytes)

	// Memory peak in bytes
	value, err := os.ReadFile("/sys/fs/cgroup/memory/memory.max_usage_in_bytes")
	valueInt, err1 := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	if err == nil && err1 == nil {
		memory.UsagePeak = valueInt
	}

	return memory
}

func networkState() map[string]api.InstanceStateNetwork {
	result := map[string]api.InstanceStateNetwork{}

	ifs, err := net.Interfaces()
	if err != nil {
		logger.Errorf("Failed to retrieve network interfaces: %v", err)
		return result
	}

	for _, iface := range ifs {
		network := api.InstanceStateNetwork{
			Addresses: []api.InstanceStateNetworkAddress{},
			Counters:  api.InstanceStateNetworkCounters{},
		}

		network.Hwaddr = iface.HardwareAddr.String()
		network.Mtu = iface.MTU

		if iface.Flags&net.FlagUp != 0 {
			network.State = "up"
		} else {
			network.State = "down"
		}

		if iface.Flags&net.FlagBroadcast != 0 {
			network.Type = "broadcast"
		} else if iface.Flags&net.FlagLoopback != 0 {
			network.Type = "loopback"
		} else if iface.Flags&net.FlagPointToPoint != 0 {
			network.Type = "point-to-point"
		} else {
			network.Type = "unknown"
		}

		// Counters
		value, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/tx_bytes", iface.Name))
		valueInt, err1 := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.BytesSent = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/rx_bytes", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.BytesReceived = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/tx_packets", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.PacketsSent = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/rx_packets", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.PacketsReceived = valueInt
		}

		// Addresses
		addrs, _ := iface.Addrs()

		for _, addr := range addrs {
			addressFields := strings.Split(addr.String(), "/")

			networkAddress := api.InstanceStateNetworkAddress{
				Address: addressFields[0],
				Netmask: addressFields[1],
			}

			scope := "global"
			if strings.HasPrefix(addressFields[0], "127") {
				scope = "local"
			}

			if addressFields[0] == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(addressFields[0], "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(addressFields[0], "fe80:") {
				scope = "link"
			}

			networkAddress.Scope = scope

			if strings.Contains(addressFields[0], ":") {
				networkAddress.Family = "inet6"
			} else {
				networkAddress.Family = "inet"
			}

			network.Addresses = append(network.Addresses, networkAddress)
		}

		result[iface.Name] = network
	}

	return result
}

func processesState() int64 {
	pids := []int64{1}

	// Go through the pid list, adding new pids at the end so we go through them all
	for i := 0; i < len(pids); i++ {
		fname := fmt.Sprintf("/proc/%d/task/%d/children", pids[i], pids[i])
		fcont, err := os.ReadFile(fname)
		if err != nil {
			// the process terminated during execution of this loop
			continue
		}

		content := strings.Split(string(fcont), " ")
		for j := 0; j < len(content); j++ {
			pid, err := strconv.ParseInt(content[j], 10, 64)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return int64(len(pids))
}
