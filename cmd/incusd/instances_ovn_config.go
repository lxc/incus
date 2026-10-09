package main

import (
	"fmt"
	"maps"
	"strings"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/device/nictype"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// ovnNICNewInstanceConfig removes host allocations that belong to another instance record.
func ovnNICNewInstanceConfig(config map[string]string) map[string]string {
	result := maps.Clone(config)
	for key := range config {
		if !strings.HasPrefix(key, "volatile.") {
			continue
		}

		for _, suffix := range []string{".last_state.ovn.host", ".last_state.ovn.physical"} {
			if !strings.HasSuffix(key, suffix) {
				continue
			}

			name := strings.TrimSuffix(strings.TrimPrefix(key, "volatile."), suffix)
			if name == "" {
				continue
			}

			for _, field := range db.OVNNICStopVolatileKeys() {
				delete(result, "volatile."+name+"."+field)
			}
		}
	}

	return result
}

// ovnNICNewCopyConfig gives a first refresh copy its own identity while preserving internal moves.
func ovnNICNewCopyConfig(config map[string]string, sourceUUID string, refreshRequested bool) map[string]string {
	result := ovnNICNewInstanceConfig(config)
	if refreshRequested && result["volatile.uuid"] == sourceUUID {
		delete(result, "volatile.uuid")
	}

	return result
}

// instanceOVNLiveProjectMoveAdmission runs before capturing or dispatching migration effects.
func instanceOVNLiveProjectMoveAdmission(s *state.State, inst instance.Instance, req api.InstancePost) error {
	if !req.Live || !inst.IsRunning() || req.Project == "" || req.Project == inst.Project().Name {
		return nil
	}

	for name, dev := range inst.ExpandedDevices() {
		if dev["type"] != "nic" {
			continue
		}

		typ, err := nictype.NICType(s, inst.Project().Name, dev)
		if err != nil {
			return err
		}

		if typ == "ovn" {
			return fmt.Errorf("Instance must be stopped to move across projects with OVN NIC %q", name)
		}
	}

	return nil
}
