package acl

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
)

// BridgeUpdateACLs forces the update of all NIC devices who have the changed ACL applied.
func BridgeUpdateACLs(s *state.State, l logger.Logger, aclProjectName string, aclNetDevices map[string]NetworkACLUsage) error {
	// Update of the bridge NICs affected by the ACL change
	for _, aclNetDevice := range aclNetDevices {
		inst, err := instance.LoadByProjectAndName(s, aclProjectName, aclNetDevice.InstanceName)
		if err != nil {
			return err
		}

		// Skip remote instances.
		if inst.Location() != "" && inst.Location() != s.ServerName {
			continue
		}

		// Skip stopped instances
		if !inst.IsRunning() {
			continue
		}

		// Trigger the device update.
		err = inst.ReloadDevice(aclNetDevice.DeviceName)
		if err != nil {
			return fmt.Errorf("Failed to trigger device update for device %q of instance %q in project %q: %w", aclNetDevice.DeviceName, inst.Name(), inst.Project().Name, err)
		}
	}

	return nil
}
