package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/internal/linux"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/fsmonitor/drivers"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// unixIsOurDeviceType checks that device file type matches what we are expecting in the config.
func unixIsOurDeviceType(config deviceConfig.Device, dType string) bool {
	if config["type"] == "unix-char" && dType == "c" {
		return true
	}

	if config["type"] == "unix-block" && dType == "b" {
		return true
	}

	return false
}

type unixCommon struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *unixCommon) isRequired() bool {
	// Defaults to required.
	return util.IsTrueOrEmpty(d.config["required"])
}

// validateConfig checks the supplied config for correctness.
func (d *unixCommon) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"source": func(value string) error {
			if value == "" {
				return nil
			}

			if strings.HasPrefix(value, d.state.DevMonitor.PrefixPath()) {
				return nil
			}

			return &drivers.ErrInvalidPath{PrefixPath: d.state.DevMonitor.PrefixPath()}
		},
		"path":     validate.IsAny,
		"major":    unixValidDeviceNum,
		"minor":    unixValidDeviceNum,
		"uid":      unixValidUserID,
		"gid":      unixValidUserID,
		"mode":     unixValidOctalFileMode,
		"required": validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["source"] == "" && d.config["path"] == "" {
		return fmt.Errorf("Unix device entry is missing the required \"source\" or \"path\" property")
	}

	return nil
}

// Register is run after the device is started or on daemon startup.
func (d *unixCommon) Register() error {
	// Don't register for hot plug events if the device is required.
	if d.isRequired() {
		return nil
	}

	// Extract variables needed to run the event hook so that the reference to this device
	// struct is not needed to be kept in memory.
	devicesPath := d.inst.DevicesPath()
	devConfig := d.config
	deviceName := d.name
	state := d.state

	// Handler for when a Unix event occurs.
	f := func(e UnixEvent) (*deviceConfig.RunConfig, error) {
		// Check if the event is for a device file that this device wants.
		if unixDeviceSourcePath(devConfig) != e.Path {
			return nil, nil
		}

		// Derive the host side path for the instance device file.
		ourPrefix := deviceJoinPath("unix", deviceName)
		relativeDestPath := strings.TrimPrefix(unixDeviceDestPath(devConfig), "/")
		devName := linux.PathNameEncode(deviceJoinPath(ourPrefix, relativeDestPath))
		devPath := filepath.Join(devicesPath, devName)

		runConf := deviceConfig.RunConfig{}

		if e.Action == "add" {
			// Skip if host side instance device file already exists.
			if util.PathExists(devPath) {
				return nil, nil
			}

			// Get the file type and ensure it matches what the user was expecting.
			dType, _, _, err := unixDeviceAttributes(e.Path)
			if err != nil {
				if os.IsNotExist(err) {
					// Skip if host side source device doesn't exist.
					// This could be an event for the parent directory being added.
					return nil, nil
				}

				return nil, fmt.Errorf("Failed getting device attributes: %w", err)
			}

			if !unixIsOurDeviceType(d.config, dType) {
				return nil, fmt.Errorf("Path specified is not a %s device", d.config["type"])
			}

			err = unixDeviceSetup(state, devicesPath, "unix", deviceName, devConfig, true, &runConf)
			if err != nil {
				return nil, err
			}
		} else if e.Action == "remove" {
			// Skip if host side instance device file doesn't exist.
			if !util.PathExists(devPath) {
				return nil, nil
			}

			err := unixDeviceRemove(devicesPath, "unix", deviceName, relativeDestPath, &runConf)
			if err != nil {
				return nil, err
			}

			// Add a post hook function to remove the specific USB device file after unmount.
			runConf.PostHooks = []func() error{func() error {
				err := unixDeviceDeleteFiles(state, devicesPath, "unix", deviceName, relativeDestPath)
				if err != nil {
					return fmt.Errorf("Failed to delete files for device '%s': %w", deviceName, err)
				}

				return nil
			}}
		}

		return &runConf, nil
	}

	// Register the handler function against the device's source path.
	subPath := unixDeviceSourcePath(devConfig)
	err := unixRegisterHandler(d.state, d.inst, d.name, subPath, f)
	if err != nil {
		return err
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *unixCommon) Start() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}
	srcPath := unixDeviceSourcePath(d.config)

	// If device file already exists on system, proceed to add it whether its required or not.
	dType, _, _, err := unixDeviceAttributes(srcPath)
	if err == nil {
		// Ensure device type matches what the device config is expecting.
		if !unixIsOurDeviceType(d.config, dType) {
			return nil, fmt.Errorf("Path specified is not a %s device", d.config["type"])
		}

		err = unixDeviceSetup(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, true, &runConf)
		if err != nil {
			return nil, err
		}
	} else {
		// If the device file doesn't exist on the system, but major & minor numbers have
		// been provided in the config then we can go ahead and create the device anyway.
		if d.config["major"] != "" && d.config["minor"] != "" {
			err := unixDeviceSetup(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, true, &runConf)
			if err != nil {
				return nil, err
			}
		} else if d.isRequired() {
			// If the file is missing and the device is required then we cannot proceed.
			return nil, fmt.Errorf("The required device path doesn't exist and the major and minor settings are not specified")
		}
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *unixCommon) Stop() (*deviceConfig.RunConfig, error) {
	// Unregister any Unix event handlers for this device.
	err := unixUnregisterHandler(d.state, d.inst, d.name)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err = unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *unixCommon) postStop() error {
	// Remove host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
	}

	return nil
}
