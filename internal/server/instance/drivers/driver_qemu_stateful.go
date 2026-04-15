package drivers

import (
	"encoding/json"
	"errors"
)

type qemuBootState struct {
	Version int `json:"version"`

	CPUType     string           `json:"cpu_type"`
	CPUTopology *qemuCPUTopology `json:"cpu_topology"`

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

	// If not stateful, we're done here.
	if !d.CanLiveMigrate() {
		return &bs, nil
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

func (d *qemu) saveBootState(bs qemuBootState) error {
	// Build a list of volatile changes.
	volatileSet := make(map[string]string)

	// Clear all keys.
	volatileSet["volatile.vm.boot_state"] = ""

	volatileSet["volatile.vm.definition"] = ""
	volatileSet["volatile.vm.hotplug.memory"] = ""

	// If stateful isn't enabled, we're done.
	if !d.CanLiveMigrate() {
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
