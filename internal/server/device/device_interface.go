package device

import (
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
)

// VolatileSetter is a function that accepts one or more key/value strings to save into the
// config for this instance. It should add the volatile device name prefix to each key when saving.
type VolatileSetter func(map[string]string) error

// VolatileGetter is a function that retrieves any key/value string that exists in the database
// config for this instance. It should only return keys that match the volatile device name prefix,
// and should remove the prefix before being returned.
type VolatileGetter func() map[string]string

// Type represents a device type.
type Type interface {
	// CanHotPlug returns true if the device can be managed whilst instance is running.
	CanHotPlug() bool

	// CanMigrate returns true if the device should work properly on any cluster member.
	CanMigrate() bool

	// UpdatableFields returns a slice of config fields that can be updated. If only fields in this list have
	// changed then Update() is called rather triggering a device remove & add.
	UpdatableFields(oldDevice Type) []string
}

// Device represents a device that can be added to an instance.
type Device interface {
	Type

	Config() deviceConfig.Device
	Name() string

	// Add performs any host-side setup when a device is added to an instance.
	// It is called irrespective of whether the instance is running or not.
	Add() error

	// PreStartCheck indicates if the device is available for starting.
	PreStartCheck() error

	// Start peforms any host-side configuration required to start the device for the instance.
	// This can be when a device is plugged into a running instance or the instance is starting.
	// Returns run-time configuration needed for configuring the instance with the new device.
	Start() (*deviceConfig.RunConfig, error)

	// Register provides the ability for a device to subcribe to daemon generated events.
	// It is called after a device is started (after Start()) or on daemon startup.
	Register() error

	// Update performs host-side modifications for a device based on the difference between the
	// current config and previous devices config supplied as an argument. This called if the
	// only config fields that have changed are supplied in the list returned from UpdatableFields().
	// The function also accepts a boolean indicating whether the instance is running or not.
	Update(oldDevices deviceConfig.Devices, running bool) error

	// Stop performs any host-side cleanup required when a device is removed from an instance,
	// either due to unplugging it from a running instance or instance is being shutdown.
	// Returns run-time configuration needed for detaching the device from the instance.
	Stop() (*deviceConfig.RunConfig, error)

	// Remove performs any host-side cleanup when a device is removed from an instance.
	Remove() error
}

// device represents a sealed interface that implements Device, but also contains some internal
// setup functions for a Device that should only be called by device.New() to avoid exposing devices
// that are not in a known configured state. This is separate from the Device interface so that
// Devices created outside of the device package can be used externally, but ensures that any devices
// created by the device package will only be accessible after being configured properly by New().
type device interface {
	Device

	// init stores the Instance, daemon State and Config into device and performs any setup.
	init(instance.Instance, *state.State, string, deviceConfig.Device, VolatileGetter, VolatileSetter) error

	// validateConfig checks Config stored by init() is valid for the instance type.
	validateConfig(instance.ConfigReader) error
}

// NICState provides the ability to access NIC state.
type NICState interface {
	State() (*api.InstanceStateNetwork, error)
}
