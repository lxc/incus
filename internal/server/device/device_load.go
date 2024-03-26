package device

import (
	"fmt"

	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/device/nictype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/validate"
)

// newByType returns a new unitialized device based of the type indicated by the project and device config.
func newByType(state *state.State, projectName string, conf deviceConfig.Device) (device, error) {
	if conf["type"] == "" {
		return nil, fmt.Errorf("Missing device type in config")
	}

	// NIC type is required to lookup network devices.
	nicType, err := nictype.NICType(state, projectName, conf)
	if err != nil {
		return nil, err
	}

	// Lookup device type implementation.
	var dev device
	switch conf["type"] {
	case "nic":
		switch nicType {
		case "physical":
			dev = &nicPhysical{}
		case "ipvlan":
			dev = &nicIPVLAN{}
		case "p2p":
			dev = &nicP2P{}
		case "bridged":
			dev = &nicBridged{}
		case "routed":
			dev = &nicRouted{}
		case "macvlan":
			dev = &nicMACVLAN{}
		case "sriov":
			dev = &nicSRIOV{}
		case "ovn":
			dev = &nicOVN{}
		}

	case "infiniband":
		switch nicType {
		case "physical":
			dev = &infinibandPhysical{}
		case "sriov":
			dev = &infinibandSRIOV{}
		}

	case "gpu":
		switch conf["gputype"] {
		case "mig":
			dev = &gpuMIG{}
		case "mdev":
			dev = &gpuMdev{}
		case "sriov":
			dev = &gpuSRIOV{}
		default:
			dev = &gpuPhysical{}
		}

	case "proxy":
		dev = &proxy{}
	case "usb":
		dev = &usb{}
	case "unix-char", "unix-block":
		dev = &unixCommon{}
	case "unix-hotplug":
		dev = &unixHotplug{}
	case "disk":
		dev = &disk{}
	case "none":
		dev = &none{}
	case "tpm":
		dev = &tpm{}
	case "pci":
		dev = &pci{}
	}

	// Check a valid device type has been found.
	if dev == nil {
		return nil, ErrUnsupportedDevType
	}

	return dev, nil
}

// load instantiates a device and initializes its internal state. It does not validate the config supplied.
func load(inst instance.Instance, state *state.State, projectName string, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (device, error) {
	// Warning: When validating a profile, inst is expected to be provided as nil.
	dev, err := newByType(state, projectName, conf)
	if err != nil {
		return nil, fmt.Errorf("Failed loading device %q: %w", name, err)
	}

	// Setup the device's internal variables.
	err = dev.init(inst, state, name, conf, volatileGet, volatileSet)
	if err != nil {
		return nil, fmt.Errorf("Failed loading device %q: %w", name, err)
	}

	return dev, nil
}

// New instantiates a new device struct, validates the supplied config and sets it into the device.
// If the device type is valid, but the other config validation fails then an instantiated device
// is still returned with the validation error. If an unknown device is requested or the device is
// not compatible with the instance type then an ErrUnsupportedDevType error is returned.
// Note: The supplied config may be modified during validation to enrich. If this is not desired, supply a copy.
func New(inst instance.Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (Device, error) {
	dev, err := load(inst, state, inst.Project().Name, name, conf, volatileGet, volatileSet)
	if err != nil {
		return nil, err
	}

	// We still return the instantiated device here, as in some scenarios the caller
	// may still want to use the device (such as when stopping or removing) even if
	// the config validation has failed.

	err = validate.IsDeviceName(name)
	if err != nil {
		return dev, err
	}

	err = dev.validateConfig(inst)
	if err != nil {
		return dev, err
	}

	return dev, nil
}

// Validate checks a device's config is valid. This only requires an instance.ConfigReader rather than an full
// blown instance to allow profile devices to be validated too.
// Note: The supplied config may be modified during validation to enrich. If this is not desired, supply a copy.
func Validate(instConfig instance.ConfigReader, state *state.State, name string, conf deviceConfig.Device) error {
	err := validate.IsDeviceName(name)
	if err != nil {
		return err
	}

	dev, err := load(nil, state, instConfig.Project().Name, name, conf, nil, nil)
	if err != nil {
		return err
	}

	return dev.validateConfig(instConfig)
}

// LoadByType loads a device by type based on its project and config.
// It does not validate config beyond the type fields.
func LoadByType(state *state.State, projectName string, conf deviceConfig.Device) (Type, error) {
	dev, err := newByType(state, projectName, conf)
	if err != nil {
		return nil, fmt.Errorf("Failed loading device type: %w", err)
	}

	return dev, nil
}
