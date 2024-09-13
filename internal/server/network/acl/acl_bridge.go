package acl

import (
	"github.com/lxc/incus/v6/internal/server/db"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
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

		// Skip stopped instances
		if !inst.IsRunning() {
			continue
		}

		devices := inst.LocalDevices().CloneNative()

		// Test if device named aclNetDevice.DeviceName is present in the instance
		_, found := devices[aclNetDevice.DeviceName]

		if !found {
			continue
		}

		l.Debug("Forcing update to NIC device to update ACL rules", logger.Ctx{"instance": aclNetDevice.InstanceName, "nicDevice": aclNetDevice.DeviceName})

		// Set the "force_update" key to force the device to be updated due to difference in the config
		devices[aclNetDevice.DeviceName]["force_update"] = "true"

		args := db.InstanceArgs{
			Architecture: inst.Architecture(),
			Config:       inst.ExpandedConfig(),
			Description:  inst.Description(),
			Devices:      deviceConfig.NewDevices(devices),
			Ephemeral:    inst.IsEphemeral(),
			Profiles:     inst.Profiles(),
			Project:      inst.Project().Name,
		}

		err = inst.Update(args, false)
		if err != nil {
			return err
		}
	}

	return nil
}
