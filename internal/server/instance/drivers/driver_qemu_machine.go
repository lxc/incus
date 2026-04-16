package drivers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/instance/drivers/qemudefault"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

type qemuCPUTopology struct {
	Sockets int `json:"sockets"`
	Cores   int `json:"cores"`
	Threads int `json:"threads"`
	VCPUs   map[uint64]uint64
	Nodes   map[uint64][]uint64
}

// cpuTopology sets up the qemuCPUTopology struct based on configured CPU limits, host system and guest OS.
func (d *qemu) cpuTopology() (*qemuCPUTopology, error) {
	topology := &qemuCPUTopology{}

	// Set default to 1 vCPU.
	limit := d.expandedConfig["limits.cpu"]

	if limit == "" {
		limit = "1"
	}

	// Check if pinned or floating.
	nrLimit, err := strconv.Atoi(limit)
	if err == nil {
		// We're not dealing with a pinned setup.
		topology.Sockets = 1
		topology.Cores = nrLimit
		topology.Threads = 1

		return topology, nil
	}

	// Get CPU topology.
	cpus, err := resources.GetCPU()
	if err != nil {
		return nil, err
	}

	// Expand the pins.
	pins, err := resources.ParseCpuset(limit)
	if err != nil {
		return nil, err
	}

	// Match tracking.
	vcpus := map[uint64]uint64{}
	sockets := map[uint64][]uint64{}
	cores := map[uint64][]uint64{}
	numaNodes := map[uint64][]uint64{}

	// Go through the physical CPUs looking for matches.
	i := uint64(0)
	for _, cpu := range cpus.Sockets {
		for _, core := range cpu.Cores {
			for _, thread := range core.Threads {
				for _, pin := range pins {
					if thread.ID == int64(pin) {
						// Found a matching CPU.
						vcpus[i] = uint64(pin)

						// Track cores per socket.
						_, ok := sockets[cpu.Socket]
						if !ok {
							sockets[cpu.Socket] = []uint64{}
						}

						if !slices.Contains(sockets[cpu.Socket], core.Core) {
							sockets[cpu.Socket] = append(sockets[cpu.Socket], core.Core)
						}

						// Track threads per core.
						_, ok = cores[core.Core]
						if !ok {
							cores[core.Core] = []uint64{}
						}

						if !slices.Contains(cores[core.Core], thread.Thread) {
							cores[core.Core] = append(cores[core.Core], thread.Thread)
						}

						// Record NUMA node for thread.
						_, ok = cores[core.Core]
						if !ok {
							numaNodes[thread.NUMANode] = []uint64{}
						}

						numaNodes[thread.NUMANode] = append(numaNodes[thread.NUMANode], i)

						i++
					}
				}
			}
		}
	}

	// Confirm we're getting the expected number of CPUs.
	if len(pins) != len(vcpus) {
		return nil, fmt.Errorf("Unavailable CPUs requested: %s", limit)
	}

	// Validate the topology.
	valid := true
	nrSockets := 0
	nrCores := 0
	nrThreads := 0

	// Confirm that there is no balancing inconsistencies.
	countCores := -1
	for _, cores := range sockets {
		if countCores != -1 && len(cores) != countCores {
			valid = false
			break
		}

		countCores = len(cores)
	}

	countThreads := -1
	for _, threads := range cores {
		if countThreads != -1 && len(threads) != countThreads {
			valid = false
			break
		}

		countThreads = len(threads)
	}

	// Check against double listing of CPU.
	if len(sockets)*countCores*countThreads != len(vcpus) {
		valid = false
	}

	// Build up the topology.
	if valid {
		// Valid topology.
		nrSockets = len(sockets)
		nrCores = countCores
		nrThreads = countThreads
	} else {
		d.logger.Warn("Instance uses a CPU pinning profile which doesn't match hardware layout")

		// Fallback on pretending everything are cores.
		nrSockets = 1
		nrCores = len(vcpus)
		nrThreads = 1
	}

	// Prepare struct.
	topology.Sockets = nrSockets
	topology.Cores = nrCores
	topology.Threads = nrThreads
	topology.VCPUs = vcpus
	topology.Nodes = numaNodes

	return topology, nil
}

// cpuType generates the QEMU cpu flag based on the CPU topology, guest OS and host system.
func (d *qemu) cpuType(bs *qemuBootState) (string, error) {
	// Determine additional CPU flags.
	cpuExtensions := []string{}

	if d.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		// If using Linux 5.10 or later, use HyperV optimizations.
		minVer, _ := version.NewDottedVersion("5.10.0")
		if d.state.OS.KernelVersion.Compare(minVer) >= 0 && !d.CanLiveMigrate() {
			// x86_64 can use hv_time to improve Windows guest performance.
			cpuExtensions = append(cpuExtensions, "hv_passthrough")
		}

		// x86_64 requires the use of topoext when SMT is used.
		if bs.CPUTopology.Threads > 1 {
			cpuExtensions = append(cpuExtensions, "topoext")
		}
	}

	cpuType := "host"

	// Handle CPU flags.
	if d.state.ServerClustered && d.CanLiveMigrate() {
		// Get the cluster group config.
		var groupConfig map[string]string
		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get the group name.
			clusterGroupName := d.localConfig["volatile.cluster.group"]
			if clusterGroupName == "" {
				clusterGroupName = "default"
			}

			// Try to get the cluster group.
			group, err := dbCluster.GetClusterGroup(ctx, tx.Tx(), clusterGroupName)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			// Fallback to default group.
			if errors.Is(err, sql.ErrNoRows) && clusterGroupName != "default" {
				group, err = dbCluster.GetClusterGroup(ctx, tx.Tx(), "default")
				if err != nil {
					return err
				}
			}

			// Get the config.
			groupConfig, err = dbCluster.GetClusterGroupConfig(ctx, tx.Tx(), group.ID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return "", err
		}

		// Get the local architecture name.
		archName, err := osarch.ArchitectureName(d.architecture)
		if err != nil {
			return "", err
		}

		// Set the cpu type and extensions.
		groupConfigBaseline := fmt.Sprintf("instances.vm.cpu.%s.baseline", archName)
		groupConfigFlags := fmt.Sprintf("instances.vm.cpu.%s.flags", archName)

		if groupConfig[groupConfigBaseline] != "" {
			// Apply group config if present.
			cpuType = groupConfig[groupConfigBaseline]
			cpuExtensions = append(cpuExtensions, util.SplitNTrimSpace(groupConfig[groupConfigFlags], ",", -1, true)...)
		} else if d.architecture == osarch.ARCH_64BIT_INTEL_X86 {
			// Apply automatic handling if on x86_64.
			cpuFlags, err := GetClusterCPUFlags(context.TODO(), d.state, nil, archName)
			if err != nil {
				return "", err
			}

			cpuType = "kvm64"
			cpuExtensions = append(cpuExtensions, cpuFlags...)
		}
	}

	// Get the feature flags.
	info := DriverStatuses()[instancetype.VM].Info
	_, nested := info.Features["nested"]

	// Add +invtsc for fast TSC on x86 when not expected to be migratable and not nested.
	if !nested && d.architecture == osarch.ARCH_64BIT_INTEL_X86 && !d.CanLiveMigrate() {
		cpuExtensions = append(cpuExtensions, "migratable=no", "+invtsc")
	}

	if len(cpuExtensions) > 0 {
		cpuType += "," + strings.Join(cpuExtensions, ",")
	}

	return cpuType, nil
}

type qemuMemoryTopology struct {
	Base  int64   `json:"base"`
	Max   int64   `json:"max"`
	Extra []int64 `json:"extra"`
}

func (d *qemu) memoryTopology(bs *qemuBootState) (*qemuMemoryTopology, error) {
	// Configure memory limit.
	memSize := d.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = qemudefault.MemSize // Default if no memory limit specified.
	}

	memSizeBytes, err := ParseMemoryStr(memSize)
	if err != nil {
		return nil, fmt.Errorf("Invalid limits.memory value %q: %w", memSize, err)
	}

	// Set hotplug limits.
	// kvm64 has a limit of 39 bits for aarch64 and 40 bits on x86_64, so just limit everyone to 39 bits (512GB).
	// Other types we don't know so just don't allow hotplug.

	var maxMemoryBytes int64
	cpuPhysBits := uint64(39)

	limitsMemoryHotplug := d.expandedConfig["limits.memory.hotplug"]
	memoryHotplugEnabled := !util.IsFalse(limitsMemoryHotplug)

	if d.GuestOS() == "freebsd" {
		memoryHotplugEnabled = false

		// We handle the empty value a bit differently here, as FreeBSD doesn’t have memory hotplug.
		if !util.IsFalseOrEmpty(limitsMemoryHotplug) {
			return nil, errors.New("FreeBSD doesn't support setting 'limits.memory.hotplug'")
		}
	}

	cpuType := strings.Split(bs.CPUType, ",")[0]
	if (cpuType == "host" || cpuType == "kvm64") && memoryHotplugEnabled {
		if !util.IsTrueOrEmpty(limitsMemoryHotplug) {
			maxMemoryBytes, err = units.ParseByteSizeString(limitsMemoryHotplug)
			if err != nil {
				return nil, err
			}

			if maxMemoryBytes < memSizeBytes {
				return nil, errors.New("'limits.memory.hotplug' value should be greater than or equal to 'limits.memory'")
			}
		}

		if maxMemoryBytes == 0 {
			// Attempt to get the CPU physical address space limits.
			cpu, err := resources.GetCPU()
			if err != nil {
				return nil, err
			}

			var lowestPhysBits uint64

			for _, socket := range cpu.Sockets {
				if socket.AddressSizes != nil && (socket.AddressSizes.PhysicalBits < lowestPhysBits || lowestPhysBits == 0) {
					lowestPhysBits = socket.AddressSizes.PhysicalBits
				}
			}

			// If a physical address size was detected, either align it with the VM (CPU passthrough) or use it as an upper bound.
			if lowestPhysBits > 0 && (cpuType == "host" || lowestPhysBits < cpuPhysBits) {
				cpuPhysBits = lowestPhysBits
			}

			// Reduce the maximum by one bit to allow QEMU some headroom.
			cpuPhysBits--

			// Calculate the max memory limit.
			maxMemoryBytes = int64(math.Pow(2, float64(cpuPhysBits)))

			// Cap to 1TB.
			if maxMemoryBytes > 1024*1024*1024*1024 {
				maxMemoryBytes = 1024 * 1024 * 1024 * 1024
			}

			// On standalone systems, further cap to the system's total memory.
			if !d.state.ServerClustered {
				totalMemory, err := linux.DeviceTotalMemory()
				if err != nil {
					return nil, err
				}

				maxMemoryBytes = totalMemory
			}
		}

		// Allow the user to go past any expected limit.
		if maxMemoryBytes < memSizeBytes {
			maxMemoryBytes = memSizeBytes
		}
	} else {
		// Prevent memory hotplug.
		maxMemoryBytes = memSizeBytes
	}

	// Create the struct.
	memInfo := &qemuMemoryTopology{}
	memInfo.Base = memSizeBytes
	memInfo.Max = maxMemoryBytes

	return memInfo, nil
}
