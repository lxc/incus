//go:build linux && cgo

package device

import (
	"fmt"
	"strings"

	"github.com/jochenvg/go-udev"

	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// unixHotplugIsOurDevice indicates whether the unixHotplug device event qualifies as part of our device.
// This function is not defined against the unixHotplug struct type so that it can be used in event
// callbacks without needing to keep a reference to the unixHotplug device struct.
func unixHotplugIsOurDevice(config deviceConfig.Device, unixHotplug *UnixHotplugEvent) bool {
	// Check if event matches criteria for this device, if not return.
	if (config["vendorid"] != "" && config["vendorid"] != unixHotplug.Vendor) || (config["productid"] != "" && config["productid"] != unixHotplug.Product) {
		return false
	}

	return true
}

type unixHotplug struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *unixHotplug) isRequired() bool {
	// Defaults to not required.
	return util.IsTrue(d.config["required"])
}

// validateConfig checks the supplied config for correctness.
func (d *unixHotplug) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"vendorid":  validate.Optional(validate.IsDeviceID),
		"productid": validate.Optional(validate.IsDeviceID),
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
		"required":  validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["vendorid"] == "" && d.config["productid"] == "" {
		return fmt.Errorf("Unix hotplug devices require a vendorid or a productid")
	}

	return nil
}

// Register is run after the device is started or on daemon startup.
func (d *unixHotplug) Register() error {
	// Extract variables needed to run the event hook so that the reference to this device
	// struct is not needed to be kept in memory.
	devicesPath := d.inst.DevicesPath()
	devConfig := d.config
	deviceName := d.name
	state := d.state

	// Handler for when a UnixHotplug event occurs.
	f := func(e UnixHotplugEvent) (*deviceConfig.RunConfig, error) {
		runConf := deviceConfig.RunConfig{}

		if e.Action == "add" {
			if !unixHotplugIsOurDevice(devConfig, &e) {
				return nil, nil
			}

			if e.Subsystem == "block" {
				err := unixDeviceSetupBlockNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
				if err != nil {
					return nil, err
				}
			} else {
				err := unixDeviceSetupCharNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		} else if e.Action == "remove" {
			relativeTargetPath := strings.TrimPrefix(e.Path, "/")
			err := unixDeviceRemove(devicesPath, "unix", deviceName, relativeTargetPath, &runConf)
			if err != nil {
				return nil, err
			}

			// Add a post hook function to remove the specific unix hotplug device file after unmount.
			runConf.PostHooks = []func() error{func() error {
				err := unixDeviceDeleteFiles(state, devicesPath, "unix", deviceName, relativeTargetPath)
				if err != nil {
					return fmt.Errorf("Failed to delete files for device '%s': %w", deviceName, err)
				}

				return nil
			}}
		}

		runConf.Uevents = append(runConf.Uevents, e.UeventParts)

		return &runConf, nil
	}

	unixHotplugRegisterHandler(d.inst, d.name, f)

	return nil
}

// Start is run when the device is added to the instance.
func (d *unixHotplug) Start() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	device := d.loadUnixDevice()
	if d.isRequired() && device == nil {
		return nil, fmt.Errorf("Required Unix Hotplug device not found")
	}

	if device == nil {
		return &runConf, nil
	}

	devnum := device.Devnum()
	major := uint32(devnum.Major())
	minor := uint32(devnum.Minor())

	// setup device
	var err error
	if device.Subsystem() == "block" {
		err = unixDeviceSetupBlockNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, device.Devnode(), false, &runConf)
	} else {
		err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, device.Devnode(), false, &runConf)
	}

	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *unixHotplug) Stop() (*deviceConfig.RunConfig, error) {
	unixHotplugUnregisterHandler(d.inst, d.name)

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *unixHotplug) postStop() error {
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
	}

	return nil
}

// loadUnixDevice scans the host machine for unix devices with matching product/vendor ids
// and returns the first matching device with the subsystem type char or block.
func (d *unixHotplug) loadUnixDevice() *udev.Device {
	// Find device if exists
	u := udev.Udev{}
	e := u.NewEnumerate()

	if d.config["vendorid"] != "" {
		err := e.AddMatchProperty("ID_VENDOR_ID", d.config["vendorid"])
		if err != nil {
			logger.Warn("Failed to add property to device", logger.Ctx{"property_name": "ID_VENDOR_ID", "property_value": d.config["vendorid"], "err": err})
		}
	}

	if d.config["productid"] != "" {
		err := e.AddMatchProperty("ID_MODEL_ID", d.config["productid"])
		if err != nil {
			logger.Warn("Failed to add property to device", logger.Ctx{"property_name": "ID_MODEL_ID", "property_value": d.config["productid"], "err": err})
		}
	}

	err := e.AddMatchIsInitialized()
	if err != nil {
		logger.Warn("Failed to add initialized property to device", logger.Ctx{"err": err})
	}

	devices, _ := e.Devices()
	var device *udev.Device
	for i := range devices {
		device = devices[i]

		if device == nil {
			continue
		}

		devnum := device.Devnum()
		if devnum.Major() == 0 || devnum.Minor() == 0 {
			continue
		}

		if device.Devnode() == "" {
			continue
		}

		if !strings.HasPrefix(device.Subsystem(), "usb") {
			return device
		}
	}

	return nil
}
