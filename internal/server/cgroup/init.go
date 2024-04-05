package cgroup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var cgControllers = map[string]Backend{}
var cgNamespace bool

// Layout determines the cgroup layout on this system.
type Layout int

const (
	// CgroupsDisabled indicates that cgroups are not supported.
	CgroupsDisabled Layout = iota
	// CgroupsUnified indicates that this is a pure cgroup2 layout.
	CgroupsUnified
	// CgroupsHybrid indicates that this is a mixed cgroup1 and cgroup2 layout.
	CgroupsHybrid
	// CgroupsLegacy indicates that this is a pure cgroup1 layout.
	CgroupsLegacy
)

var cgLayout Layout

// Info contains system cgroup information.
type Info struct {
	// Layout is one of CgroupsDisabled, CgroupsUnified, CgroupsHybrid, CgroupsLegacy
	Layout Layout

	// Namespacing indicates support for the cgroup namespace
	Namespacing bool
}

// GetInfo returns basic system cgroup information.
func GetInfo() Info {
	info := Info{}
	info.Namespacing = cgNamespace
	info.Layout = cgLayout

	return info
}

// Mode returns the cgroup layout name.
func (info *Info) Mode() string {
	switch info.Layout {
	case CgroupsDisabled:
		return "disabled"
	case CgroupsUnified:
		return "cgroup2"
	case CgroupsHybrid:
		return "hybrid"
	case CgroupsLegacy:
		return "legacy"
	}

	return "unknown"
}

// Resource is a generic type used to abstract resource control features
// support for the legacy and unified hierarchy.
type Resource int

const (
	// Blkio resource control.
	Blkio Resource = iota

	// BlkioWeight resource control.
	BlkioWeight

	// CPU resource control.
	CPU

	// CPUAcct resource control.
	CPUAcct

	// CPUSet resource control.
	CPUSet

	// Devices resource control.
	Devices

	// Freezer resource control.
	Freezer

	// Hugetlb resource control.
	Hugetlb

	// Memory resource control.
	Memory

	// MemoryMaxUsage resource control.
	MemoryMaxUsage

	// MemorySwap resource control.
	MemorySwap

	// MemorySwapMaxUsage resource control.
	MemorySwapMaxUsage

	// MemorySwapUsage resource control.
	MemorySwapUsage

	// MemorySwappiness resource control.
	MemorySwappiness

	// Pids resource control.
	Pids
)

// SupportsVersion indicates whether or not a given cgroup resource is
// controllable and in which type of cgroup filesystem.
func (info *Info) SupportsVersion(resource Resource) (Backend, bool) {
	switch resource {
	case Blkio:
		val, ok := cgControllers["blkio"]
		if ok {
			return val, ok
		}

		val, ok = cgControllers["io"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case BlkioWeight:
		val, ok := cgControllers["blkio.weight"]
		if ok {
			return val, ok
		}

		val, ok = cgControllers["io"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case CPU:
		val, ok := cgControllers["cpu"]
		return val, ok
	case CPUAcct:
		val, ok := cgControllers["cpuacct"]
		if ok {
			return val, ok
		}

		val, ok = cgControllers["cpu"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case CPUSet:
		val, ok := cgControllers["cpuset"]
		return val, ok
	case Devices:
		val, ok := cgControllers["devices"]
		return val, ok
	case Freezer:
		val, ok := cgControllers["freezer"]
		return val, ok
	case Hugetlb:
		val, ok := cgControllers["hugetlb"]
		return val, ok
	case Memory:
		val, ok := cgControllers["memory"]
		return val, ok
	case MemoryMaxUsage:
		val, ok := cgControllers["memory.max_usage_in_bytes"]
		return val, ok
	case MemorySwap:
		val, ok := cgControllers["memory.memsw.limit_in_bytes"]
		if ok {
			return val, ok
		}

		val, ok = cgControllers["memory.swap.max"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case MemorySwapMaxUsage:
		val, ok := cgControllers["memory.memsw.max_usage_in_bytes"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case MemorySwapUsage:
		val, ok := cgControllers["memory.memsw.usage_in_bytes"]
		if ok {
			return val, ok
		}

		val, ok = cgControllers["memory.swap.current"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case MemorySwappiness:
		val, ok := cgControllers["memory.swappiness"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	case Pids:
		val, ok := cgControllers["pids"]
		if ok {
			return val, ok
		}

		return Unavailable, false
	}

	return Unavailable, false
}

// Supports indicates whether or not a given resource is controllable.
func (info *Info) Supports(resource Resource, cgroup *CGroup) bool {
	val, ok := info.SupportsVersion(resource)
	if val == V2 && cgroup != nil && !cgroup.UnifiedCapable {
		ok = false
	}

	return ok
}

// Warnings returns a list of CGroup warnings.
func (info *Info) Warnings() []cluster.Warning {
	warnings := []cluster.Warning{}

	if !info.Supports(Blkio, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupBlkio,
			LastMessage: "disk I/O limits will be ignored",
		})
	}

	if !info.Supports(BlkioWeight, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupBlkioWeight,
			LastMessage: "disk priority will be ignored",
		})
	}

	if !info.Supports(CPU, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupCPUController,
			LastMessage: "CPU time limits will be ignored",
		})
	}

	if !info.Supports(CPUAcct, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupCPUacctController,
			LastMessage: "CPU accounting will not be available",
		})
	}

	if !info.Supports(CPUSet, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupCPUController,
			LastMessage: "CPU pinning will be ignored",
		})
	}

	if !info.Supports(Devices, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupDevicesController,
			LastMessage: "device access control won't work",
		})
	}

	if !info.Supports(Freezer, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupFreezerController,
			LastMessage: "pausing/resuming containers won't work",
		})
	}

	if !info.Supports(Hugetlb, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupHugetlbController,
			LastMessage: "hugepage limits will be ignored",
		})
	}

	if !info.Supports(Memory, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupMemoryController,
			LastMessage: "memory limits will be ignored",
		})
	}

	if !info.Supports(Pids, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupPidsController,
			LastMessage: "process limits will be ignored",
		})
	}

	if !info.Supports(MemorySwap, nil) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupMemorySwapAccounting,
			LastMessage: "swap limits will be ignored",
		})
	}

	return warnings
}

// Init initializes cgroups.
func Init() {
	_, err := os.Stat("/proc/self/ns/cgroup")
	if err == nil {
		cgNamespace = true
	}

	// Go through the list of resource controllers for Incus.
	selfCg, err := os.Open("/proc/self/cgroup")
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warnf("System doesn't appear to support CGroups")
		} else {
			logger.Errorf("Unable to load list of cgroups: %v", err)
		}

		cgLayout = CgroupsDisabled
		return
	}

	defer func() { _ = selfCg.Close() }()

	hasV1 := false
	hasV2 := false
	hasV2Root := false

	// Go through the file line by line.
	scanSelfCg := bufio.NewScanner(selfCg)
	for scanSelfCg.Scan() {
		line := strings.TrimSpace(scanSelfCg.Text())
		fields := strings.SplitN(line, ":", 3)

		// Deal with the V1 controllers.
		if fields[1] != "" {
			controllers := strings.Split(fields[1], ",")
			for _, controller := range controllers {
				cgControllers[controller] = V1
			}

			hasV1 = true
			continue
		}

		// Parse V2 controllers.
		hybridPath := filepath.Join(cgPath, "unified", "cgroup.controllers")
		dedicatedPath := ""

		controllers, err := os.Open(hybridPath)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Errorf("Unable to load cgroup.controllers")
				return
			}

			dedicatedPath = filepath.Join(cgPath, "cgroup.controllers")
			controllers, err = os.Open(dedicatedPath)
			if err != nil && !os.IsNotExist(err) {
				logger.Errorf("Unable to load cgroup.controllers")
				return
			}
		}

		if err == nil {
			unifiedControllers := map[string]Backend{}

			// Record the fact that V2 is present at all.
			unifiedControllers["unified"] = V2

			scanControllers := bufio.NewScanner(controllers)
			for scanControllers.Scan() {
				line := strings.TrimSpace(scanControllers.Text())
				for _, entry := range strings.Split(line, " ") {
					unifiedControllers[entry] = V2
				}
			}
			hasV2 = true

			if dedicatedPath != "" {
				cgControllers = unifiedControllers
				hasV2Root = true
				break
			} else {
				for k, v := range unifiedControllers {
					cgControllers[k] = v
				}
			}
		}

		_ = controllers.Close()
	}

	// Discard weird setups that apply CGroupV1 trees on top of a CGroupV2 root.
	if hasV2Root && hasV1 {
		logger.Warn("Unsupported CGroup setup detected, V1 controllers on top of V2 root")
		hasV1 = false
	}

	// Check for additional legacy cgroup features
	val, ok := cgControllers["blkio"]
	if ok && val == V1 && util.PathExists("/sys/fs/cgroup/blkio/blkio.weight") {
		cgControllers["blkio.weight"] = V1
	} else {
		val, ok := cgControllers["blkio"]
		if ok && val == V1 && util.PathExists("/sys/fs/cgroup/blkio/blkio.bfq.weight") {
			cgControllers["blkio.weight"] = V1
		}
	}

	val, ok = cgControllers["memory"]
	if ok && val == V1 {
		if util.PathExists("/sys/fs/cgroup/memory/memory.max_usage_in_bytes") {
			cgControllers["memory.max_usage_in_bytes"] = V1
		}

		if util.PathExists("/sys/fs/cgroup/memory/memory.swappiness") {
			cgControllers["memory.swappiness"] = V1
		}

		if util.PathExists("/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes") {
			cgControllers["memory.memsw.limit_in_bytes"] = V1
		}

		if util.PathExists("/sys/fs/cgroup/memory/memory.memsw.usage_in_bytes") {
			cgControllers["memory.memsw.usage_in_bytes"] = V1
		}

		if util.PathExists("/sys/fs/cgroup/memory/memory.memsw.max_usage_in_bytes") {
			cgControllers["memory.memsw.max_usage_in_bytes"] = V1
		}
	}

	val, ok = cgControllers["memory"]
	if ok && val == V2 {
		if util.PathExists("/sys/fs/cgroup/init.scope/memory.swap.max") {
			cgControllers["memory.swap.max"] = V2
		}

		if util.PathExists("/sys/fs/cgroup/init.scope/memory.swap.current") {
			cgControllers["memory.swap.current"] = V2
		}
	}

	if hasV1 && hasV2 {
		cgLayout = CgroupsHybrid
	} else if hasV1 {
		cgLayout = CgroupsLegacy
	} else if hasV2 {
		cgLayout = CgroupsUnified
	}

	// "io" and "blkio" controllers are the same thing.
	val, ok = cgControllers["io"]
	if ok {
		cgControllers["blkio"] = val
	}

	if cgLayout == CgroupsUnified {
		// With Cgroup2 devices is built-in (through eBPF).
		cgControllers["devices"] = V2

		// With Cgroup2 freezer is built-in.
		cgControllers["freezer"] = V2
	}
}
