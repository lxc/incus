package cgroup

import (
	"fmt"
	"strconv"
	"strings"
)

// DeviceSchedRebalance channel for scheduling a CPU rebalance.
var DeviceSchedRebalance = make(chan []string, 2)

// TaskSchedulerTrigger triggers a CPU rebalance.
func TaskSchedulerTrigger(srcType string, srcName string, srcStatus string) {
	// Spawn a go routine which then triggers the scheduler
	select {
	case DeviceSchedRebalance <- []string{srcType, srcName, srcStatus}:
	default:
		// Channel is full, drop the event
	}
}

// ParseCPU parses CPU allowances.
func ParseCPU(cpuAllowance string, cpuPriority string) (int64, int64, int64, error) {
	var err error

	// Max shares depending on backend.
	maxShares := int64(1024)
	if cgControllers["cpu"] == V2 {
		maxShares = 100
	}

	// Parse priority
	cpuShares := int64(0)
	cpuPriorityInt := 10
	if cpuPriority != "" {
		cpuPriorityInt, err = strconv.Atoi(cpuPriority)
		if err != nil {
			return -1, -1, -1, err
		}
	}
	cpuShares -= int64(10 - cpuPriorityInt)

	// Parse allowance
	cpuCfsQuota := int64(-1)
	cpuCfsPeriod := int64(100000)
	if cgControllers["cpu"] == V2 {
		cpuCfsPeriod = -1
	}

	if cpuAllowance != "" {
		if strings.HasSuffix(cpuAllowance, "%") {
			// Percentage based allocation
			percent, err := strconv.Atoi(strings.TrimSuffix(cpuAllowance, "%"))
			if err != nil {
				return -1, -1, -1, err
			}

			cpuShares += int64(float64(maxShares) / float64(100) * float64(percent))
		} else {
			// Time based allocation
			fields := strings.SplitN(cpuAllowance, "/", 2)
			if len(fields) != 2 {
				return -1, -1, -1, fmt.Errorf("Invalid allowance: %s", cpuAllowance)
			}

			quota, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
			if err != nil {
				return -1, -1, -1, err
			}

			period, err := strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
			if err != nil {
				return -1, -1, -1, err
			}

			// Set limit in ms
			cpuCfsQuota = int64(quota * 1000)
			cpuCfsPeriod = int64(period * 1000)
			cpuShares += maxShares
		}
	} else {
		// Default is 100%
		cpuShares += maxShares
	}

	// Deal with a potential negative score
	if cpuShares < 0 {
		cpuShares = 0
	}

	return cpuShares, cpuCfsQuota, cpuCfsPeriod, nil
}
