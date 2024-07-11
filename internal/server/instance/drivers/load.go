package drivers

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/internal/server/device"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Instance driver definitions.
var instanceDrivers = map[string]func() instance.Instance{
	"lxc":  func() instance.Instance { return &lxc{} },
	"qemu": func() instance.Instance { return &qemu{} },
}

// DriverStatus definition.
type DriverStatus struct {
	Info      instance.Info
	Warning   *cluster.Warning
	Supported bool
}

// Supported instance drivers cache variables.
var driverStatusesMu sync.Mutex
var driverStatuses map[instancetype.Type]*DriverStatus

// Temporary instance reference storage (for hooks).
var instanceRefsMu sync.Mutex
var instanceRefs map[string]instance.Instance

func init() {
	// Expose load to the instance package, to avoid circular imports.
	instance.Load = load

	// Expose validDevices to the instance package, to avoid circular imports.
	instance.ValidDevices = validDevices

	// Expose create to the instance package, to avoid circular imports.
	instance.Create = create
}

// load creates the underlying instance type struct and returns it as an Instance.
func load(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
	var inst instance.Instance
	var err error

	if args.Type == instancetype.Container {
		inst, err = lxcLoad(s, args, p)
	} else if args.Type == instancetype.VM {
		inst, err = qemuLoad(s, args, p)
	} else {
		return nil, fmt.Errorf("Invalid instance type for instance %s", args.Name)
	}

	if err != nil {
		return nil, err
	}

	return inst, nil
}

// validDevices validate instance device configs.
func validDevices(state *state.State, p api.Project, instanceType instancetype.Type, localDevices deviceConfig.Devices, expandedDevices deviceConfig.Devices) error {
	instConf := &common{
		dbType:          instanceType,
		localDevices:    localDevices.Clone(),
		expandedDevices: expandedDevices.Clone(),
		project:         p,
	}

	var checkedDevices []string

	checkDevices := func(devices deviceConfig.Devices, expanded bool) error {
		// Check each device individually using the device package.
		for deviceName, deviceConfig := range devices {
			if expanded && slices.Contains(checkedDevices, deviceName) {
				continue // Don't check the device twice if present in both local and expanded.
			}

			// Enforce a maximum name length of 64 characters.
			// This is a safe maximum allowing use for sockets and other filesystem use.
			if len(deviceName) > 64 {
				return fmt.Errorf("The maximum device name length is 64 characters")
			}

			err := device.Validate(instConf, state, deviceName, deviceConfig)
			if err != nil {
				if expanded && errors.Is(err, device.ErrUnsupportedDevType) {
					// Skip unsupported devices in expanded config.
					// This allows mixed instance type profiles to be used where some devices
					// are only supported with specific instance types.
					continue
				}

				return fmt.Errorf("Device validation failed for %q: %w", deviceName, err)
			}

			checkedDevices = append(checkedDevices, deviceName)
		}

		return nil
	}

	// Check each local device individually using the device package.
	// Use the cloned config from instConf.localDevices so that the driver cannot modify it.
	err := checkDevices(instConf.localDevices, false)
	if err != nil {
		return err
	}

	if len(expandedDevices) > 0 {
		// Check we have a root disk if in expanded validation mode.
		_, _, err := internalInstance.GetRootDiskDevice(expandedDevices.CloneNative())
		if err != nil {
			return fmt.Errorf("Failed detecting root disk device: %w", err)
		}

		// Check each expanded device individually using the device package.
		// Use the cloned config from instConf.expandedDevices so that the driver cannot modify it.
		err = checkDevices(instConf.expandedDevices, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func create(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, revert.Hook, error) {
	if args.Type == instancetype.Container {
		return lxcCreate(s, args, p)
	} else if args.Type == instancetype.VM {
		return qemuCreate(s, args, p)
	}

	return nil, nil, fmt.Errorf("Instance type invalid")
}

// DriverStatuses returns a map of DriverStatus structs for all instance type drivers.
// The first time this function is called each of the instance drivers will be probed for support and the result
// will be cached internally to make subsequent calls faster.
func DriverStatuses() map[instancetype.Type]*DriverStatus {
	driverStatusesMu.Lock()
	defer driverStatusesMu.Unlock()

	if driverStatuses != nil {
		return driverStatuses
	}

	driverStatuses = make(map[instancetype.Type]*DriverStatus, len(instanceDrivers))

	for _, instanceDriver := range instanceDrivers {
		driverStatus := &DriverStatus{}

		driverInfo := instanceDriver().Info()
		driverStatus.Info = driverInfo
		driverStatus.Supported = true

		if driverInfo.Error != nil || driverInfo.Version == "" {
			logger.Warn("Instance type not operational", logger.Ctx{"type": driverInfo.Type, "driver": driverInfo.Name, "err": driverInfo.Error})

			driverStatus.Supported = false
			driverStatus.Warning = &cluster.Warning{
				TypeCode:    warningtype.InstanceTypeNotOperational,
				LastMessage: fmt.Sprintf("%v", driverInfo.Error),
			}
		} else {
			logger.Info("Instance type operational", logger.Ctx{"type": driverInfo.Type, "driver": driverInfo.Name})
		}

		driverStatuses[driverInfo.Type] = driverStatus
	}

	return driverStatuses
}

// instanceRefGet retrieves an instance reference.
func instanceRefGet(projectName string, instName string) instance.Instance {
	instanceRefsMu.Lock()
	defer instanceRefsMu.Unlock()

	return instanceRefs[project.Instance(projectName, instName)]
}

// instanceRefSet stores a reference to an instance.
func instanceRefSet(inst instance.Instance) {
	instanceRefsMu.Lock()
	defer instanceRefsMu.Unlock()

	if instanceRefs == nil {
		instanceRefs = make(map[string]instance.Instance)
	}

	instanceRefs[project.Instance(inst.Project().Name, inst.Name())] = inst
}

// instanceRefClear removes an instance reference.
func instanceRefClear(inst instance.Instance) {
	instanceRefsMu.Lock()
	defer instanceRefsMu.Unlock()

	delete(instanceRefs, project.Instance(inst.Project().Name, inst.Name()))
}
