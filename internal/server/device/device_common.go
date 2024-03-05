package device

import (
	"fmt"
	"net"

	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/logger"
)

// deviceCommon represents the common struct for all devices.
type deviceCommon struct {
	logger      logger.Logger
	inst        instance.Instance
	name        string
	config      deviceConfig.Device
	state       *state.State
	volatileGet func() map[string]string
	volatileSet func(map[string]string) error
}

// init stores the Instance, daemon state, device name and config into device.
// It also needs to be provided with volatile get and set functions for the device to allow
// persistent data to be accessed. This is implemented as part of deviceCommon so that the majority
// of devices don't need to implement it and can just embed deviceCommon.
func (d *deviceCommon) init(inst instance.Instance, s *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) error {
	logCtx := logger.Ctx{"driver": conf["type"], "device": name}
	if inst != nil {
		logCtx["project"] = inst.Project().Name
		logCtx["instance"] = inst.Name()
	}

	d.logger = logger.AddContext(logCtx)
	d.inst = inst
	d.name = name
	d.config = conf
	d.state = s
	d.volatileGet = volatileGet
	d.volatileSet = volatileSet

	return nil
}

// Name returns the name of the device.
func (d *deviceCommon) Name() string {
	return d.name
}

// Config returns the config for the device.
func (d *deviceCommon) Config() deviceConfig.Device {
	return d.config
}

// Add returns nil error as majority of devices don't need to do any host-side setup.
func (d *deviceCommon) Add() error {
	return nil
}

// Register returns nil error as majority of devices don't need to do any event registration.
func (d *deviceCommon) Register() error {
	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running,
// Returns true if instance type is container, as majority of devices can be started/stopped when
// instance is running. If instance type is VM then returns false as this is not currently supported.
func (d *deviceCommon) CanHotPlug() bool {
	return d.inst.Type() == instancetype.Container
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *deviceCommon) CanMigrate() bool {
	return false
}

// UpdatableFields returns an empty list of updatable fields as most devices do not support updates.
func (d *deviceCommon) UpdatableFields(oldDevice Type) []string {
	return []string{}
}

// PreStartCheck indicates if the device is available for starting.
func (d *deviceCommon) PreStartCheck() error {
	return nil
}

// Update returns an ErrCannotUpdate error as most devices do not support updates.
func (d *deviceCommon) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	return ErrCannotUpdate
}

// Remove returns nil error as majority of devices don't need to do any host-side cleanup on delete.
func (d *deviceCommon) Remove() error {
	return nil
}

// generateHostName generates the name to use for the host side NIC interface based on the
// instances.nic.host_name setting.
// Accepts prefix argument to use with random interface generation.
// Accepts optional hwaddr MAC address to use for generating the interface name in mac mode.
// In mac mode the interface prefix is always "inc".
func (d *deviceCommon) generateHostName(prefix string, hwaddr string) (string, error) {
	hostNameMode := d.state.GlobalConfig.InstancesNICHostname()

	// Handle instances.nic.host_name mac mode if a MAC address has been supplied.
	if hostNameMode == "mac" && hwaddr != "" {
		mac, err := net.ParseMAC(hwaddr)
		if err != nil {
			return "", fmt.Errorf("Failed parsing MAC address %q: %w", hwaddr, err)
		}

		return network.MACDevName(mac), nil
	}

	// Handle instances.nic.host_name random mode or where no MAC address supplied.
	return network.RandomDevName(prefix), nil
}
