package cgroup

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/shared/logger"
)

var (
	cgControllers = map[string]bool{}
)

// Resource is a generic type used to abstract resource control features.
type Resource int

const (
	// BlkioWeight resource control.
	BlkioWeight Resource = iota

	// CPU resource control.
	CPU

	// CPUSet resource control.
	CPUSet

	// Hugetlb resource control.
	Hugetlb

	// IO resource control.
	IO

	// Memory resource control.
	Memory

	// Pids resource control.
	Pids
)

// Supports indicates whether or not a given cgroup resource is controllable.
func Supports(resource Resource) bool {
	switch resource {
	case CPU:
		return cgControllers["cpu"]
	case CPUSet:
		return cgControllers["cpuset"]
	case Hugetlb:
		return cgControllers["hugetlb"]
	case IO:
		return cgControllers["io"]
	case Memory:
		return cgControllers["memory"]
	case Pids:
		return cgControllers["pids"]
	}

	return false
}

// Warnings returns a list of CGroup warnings.
func Warnings() []cluster.Warning {
	warnings := []cluster.Warning{}

	if !Supports(CPU) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupCPUController,
			LastMessage: "CPU time limits will be ignored",
		})
	}

	if !Supports(CPUSet) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupCPUController,
			LastMessage: "CPU pinning will be ignored",
		})
	}

	if !Supports(Hugetlb) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupHugetlbController,
			LastMessage: "hugepage limits will be ignored",
		})
	}

	if !Supports(IO) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupBlkio,
			LastMessage: "disk I/O limits will be ignored",
		})
	}

	if !Supports(Memory) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupMemoryController,
			LastMessage: "memory limits will be ignored",
		})
	}

	if !Supports(Pids) {
		warnings = append(warnings, cluster.Warning{
			TypeCode:    warningtype.MissingCGroupPidsController,
			LastMessage: "process limits will be ignored",
		})
	}

	return warnings
}

// Init initializes cgroups.
func Init() {
	// Go through the list of resource controllers for Incus.
	selfCg, err := os.Open("/proc/self/cgroup")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Warnf("System doesn't appear to support CGroups")
		} else {
			logger.Errorf("Unable to load list of cgroups: %v", err)
		}

		return
	}

	defer func() { _ = selfCg.Close() }()

	// Go through the file line by line.
	scanSelfCg := bufio.NewScanner(selfCg)
	for scanSelfCg.Scan() {
		line := strings.TrimSpace(scanSelfCg.Text())
		fields := strings.SplitN(line, ":", 3)

		// Ignore all V1 controllers.
		if fields[1] != "" {
			continue
		}

		// Parse V2 controllers.
		dedicatedPath := filepath.Join(cgPath, "cgroup.controllers")
		controllers, err := os.Open(dedicatedPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.Errorf("Unable to load cgroup.controllers")
			return
		}

		if err == nil {
			scanControllers := bufio.NewScanner(controllers)
			for scanControllers.Scan() {
				line := strings.TrimSpace(scanControllers.Text())
				for _, entry := range strings.Split(line, " ") {
					cgControllers[entry] = true
				}
			}
		}

		_ = controllers.Close()
	}
}
