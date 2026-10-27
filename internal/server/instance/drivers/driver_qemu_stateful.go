package drivers

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lxc/incus/v7/internal/server/instance/drivers/qemudefault"
	"github.com/lxc/incus/v7/shared/util"
)

// qemuMachineTypeRegex matches the plain machine type names QEMU uses, keeping options out of the boot state.
var qemuMachineTypeRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// qemuCPUTokenRegex matches one model or feature flag of a QEMU CPU type.
var qemuCPUTokenRegex = regexp.MustCompile(`^[A-Za-z0-9_.+-]+(=[A-Za-z0-9_.-]+)?$`)

type qemuBootState struct {
	Version int `json:"version"`

	CPUType     string           `json:"cpu_type"`
	CPUTopology *qemuCPUTopology `json:"cpu_topology"`
	MaxCPUs     int              `json:"max_cpus,omitempty"`

	MachineType string `json:"machine_type"`

	MemoryTopology *qemuMemoryTopology `json:"memory_topology"`
}

var qemuBootStateVersion = 1

func (bs *qemuBootState) getSCSIQueues() int {
	if bs.Version < 1 {
		return 1
	}

	if bs.CPUTopology != nil {
		return bs.CPUTopology.Sockets * bs.CPUTopology.Cores
	}

	return 1
}

func (d *qemu) getBootState() (*qemuBootState, error) {
	// Prepare a new state struct.
	bs := qemuBootState{
		Version: -1,
	}

	// Check if modern tracking is available.
	if d.localConfig["volatile.vm.boot_state"] != "" {
		err := json.Unmarshal([]byte(d.localConfig["volatile.vm.boot_state"]), &bs)
		if err != nil {
			return nil, err
		}
	} else {
		// Import legacy values if available.
		if d.localConfig["volatile.vm.definition"] != "" {
			bs.MachineType = d.localConfig["volatile.vm.definition"]
		}

		if d.localConfig["volatile.vm.hotplug.memory"] != "" {
			err := json.Unmarshal([]byte(d.localConfig["volatile.vm.hotplug.memory"]), bs.MemoryTopology)
			if err != nil {
				return nil, err
			}
		}
	}

	// Check if dealing with newer state than current.
	if bs.Version > qemuBootStateVersion {
		return nil, errors.New("Received VM state is newer than what this server supports")
	}

	return &bs, nil
}

// checkBootStateLimits refuses a received boot state giving the VM more than its configured limits.
// Restricted projects can't edit the key, but a stateful transfer brings it in from an untrusted source.
func (d *qemu) checkBootStateLimits(bs *qemuBootState) error {
	if !util.IsTrue(d.project.Config["restricted"]) || d.project.Config["restricted.virtual-machines.lowlevel"] == "allow" {
		return nil
	}

	if bs.MachineType != "" && !qemuMachineTypeRegex.MatchString(bs.MachineType) {
		return fmt.Errorf("Invalid machine type %q in the boot state", bs.MachineType)
	}

	if bs.CPUType != "" {
		for _, token := range strings.Split(bs.CPUType, ",") {
			if !qemuCPUTokenRegex.MatchString(token) {
				return fmt.Errorf("Invalid CPU type %q in the boot state", bs.CPUType)
			}
		}
	}

	if bs.CPUTopology != nil {
		topology, err := d.cpuTopology()
		if err != nil {
			return err
		}

		limit := topology.Sockets * topology.Cores * topology.Threads
		count := bs.CPUTopology.Sockets * bs.CPUTopology.Cores * bs.CPUTopology.Threads
		if count != limit {
			return fmt.Errorf("Boot state CPU count %d doesn't match the limit of %d", count, limit)
		}
	}

	if bs.MemoryTopology != nil {
		memSize := d.expandedConfig["limits.memory"]
		if memSize == "" {
			memSize = qemudefault.MemSize
		}

		limit, err := ParseMemoryStr(memSize)
		if err != nil {
			return err
		}

		total := bs.MemoryTopology.Base
		for _, extra := range bs.MemoryTopology.Extra {
			if extra <= 0 {
				return errors.New("Invalid hotplugged memory size in the boot state")
			}

			total += extra
		}

		if bs.MemoryTopology.Base <= 0 || total > limit {
			return fmt.Errorf("Boot state memory of %d bytes exceeds the limit of %d bytes", total, limit)
		}
	}

	return nil
}

func (d *qemu) saveBootState(bs qemuBootState) error {
	// Build a list of volatile changes.
	volatileSet := make(map[string]string)

	// Clear all keys.
	volatileSet["volatile.vm.boot_state"] = ""

	volatileSet["volatile.vm.definition"] = ""
	volatileSet["volatile.vm.hotplug.memory"] = ""

	// If stateful isn't enabled, we're done.
	// NOTE: Can't use CanLiveMigrate here as it itself checks the boot state.
	if !util.IsTrue(d.expandedConfig["migration.stateful"]) {
		return d.VolatileSet(volatileSet)
	}

	// Serialize and save the boot state struct.
	encoded, err := json.Marshal(bs)
	if err != nil {
		return err
	}

	volatileSet["volatile.vm.boot_state"] = string(encoded)

	// Save the changes.
	return d.VolatileSet(volatileSet)
}
