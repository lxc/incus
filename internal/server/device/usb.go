package device

import (
	"fmt"
	"os"
	"path"
	"strings"

	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// usbDevPath is the path where USB devices can be enumerated.
const usbDevPath = "/sys/bus/usb/devices"

// usbIsOurDevice indicates whether the USB device event qualifies as part of our device.
// This function is not defined against the usb struct type so that it can be used in event
// callbacks without needing to keep a reference to the usb device struct.
func usbIsOurDevice(config deviceConfig.Device, usb *USBEvent) bool {
	// Check if event matches criteria for this device, if not return.
	if (config["vendorid"] != "" && config["vendorid"] != usb.Vendor) ||
		(config["productid"] != "" && config["productid"] != usb.Product) ||
		(config["serial"] != "" && config["serial"] != usb.Serial) ||
		(config["busnum"] != "" && config["busnum"] != fmt.Sprintf("%d", usb.BusNum)) ||
		(config["devnum"] != "" && config["devnum"] != fmt.Sprintf("%d", usb.DevNum)) {
		return false
	}

	return true
}

type usb struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *usb) isRequired() bool {
	// Defaults to not required.
	return util.IsTrue(d.config["required"])
}

// validateConfig checks the supplied config for correctness.
func (d *usb) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	if instConf.Architecture() == osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		return fmt.Errorf("USB devices aren't supported on s390x")
	}

	rules := map[string]func(string) error{
		"vendorid":  validate.Optional(validate.IsDeviceID),
		"productid": validate.Optional(validate.IsDeviceID),
		"serial":    validate.Optional(validate.IsAny),
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
		"required":  validate.Optional(validate.IsBool),
		"busnum":    validate.Optional(validate.IsUint32),
		"devnum":    validate.Optional(validate.IsUint32),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// Register is run after the device is started or on daemon startup.
func (d *usb) Register() error {
	// Extract variables needed to run the event hook so that the reference to this device
	// struct is not needed to be kept in memory.
	devicesPath := d.inst.DevicesPath()
	devConfig := d.config
	deviceName := d.name
	state := d.state

	// Handler for when a USB event occurs.
	f := func(e USBEvent) (*deviceConfig.RunConfig, error) {
		if !usbIsOurDevice(devConfig, &e) {
			return nil, nil
		}

		runConf := deviceConfig.RunConfig{}

		if e.Action == "add" {
			err := unixDeviceSetupCharNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
			if err != nil {
				return nil, err
			}
		} else if e.Action == "remove" {
			relativeTargetPath := strings.TrimPrefix(e.Path, "/")
			err := unixDeviceRemove(devicesPath, "unix", deviceName, relativeTargetPath, &runConf)
			if err != nil {
				return nil, err
			}

			// Add a post hook function to remove the specific USB device file after unmount.
			runConf.PostHooks = []func() error{func() error {
				err := unixDeviceDeleteFiles(state, devicesPath, "unix", deviceName, relativeTargetPath)
				if err != nil {
					return fmt.Errorf("Failed to delete files for device '%s': %w", deviceName, err)
				}

				return nil
			}}
		}

		runConf.Uevents = append(runConf.Uevents, e.UeventParts)

		// Add the USB device to runConf so that the device handler can handle physical hotplugging.
		runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
			DeviceName:     d.getUniqueDeviceNameFromUSBEvent(e),
			HostDevicePath: e.Path,
		})

		return &runConf, nil
	}

	usbRegisterHandler(d.inst, d.name, f)

	return nil
}

// Start is run when the device is added to the instance.
func (d *usb) Start() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

func (d *usb) startContainer() (*deviceConfig.RunConfig, error) {
	usbs, err := d.loadUsb()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	for _, usb := range usbs {
		if !usbIsOurDevice(d.config, &usb) {
			continue
		}

		err := unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, usb.Major, usb.Minor, usb.Path, false, &runConf)
		if err != nil {
			return nil, err
		}
	}

	if d.isRequired() && len(runConf.Mounts) <= 0 {
		return nil, fmt.Errorf("Required USB device not found")
	}

	return &runConf, nil
}

func (d *usb) startVM() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return nil, fmt.Errorf("USB devices cannot be used when migration.stateful is enabled")
	}

	usbs, err := d.loadUsb()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	for _, usb := range usbs {
		if usbIsOurDevice(d.config, &usb) {
			runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
				DeviceName:     d.getUniqueDeviceNameFromUSBEvent(usb),
				HostDevicePath: fmt.Sprintf("/dev/bus/usb/%03d/%03d", usb.BusNum, usb.DevNum),
			})
		}
	}

	if d.isRequired() && len(runConf.USBDevice) <= 0 {
		return nil, fmt.Errorf("Required USB device not found")
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *usb) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	usbs, err := d.loadUsb()
	if err != nil {
		return nil, err
	}

	for _, usb := range usbs {
		if usbIsOurDevice(d.config, &usb) {
			runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
				DeviceName:     d.getUniqueDeviceNameFromUSBEvent(usb),
				HostDevicePath: fmt.Sprintf("/dev/bus/usb/%03d/%03d", usb.BusNum, usb.DevNum),
			})
		}
	}

	if d.inst.Type() == instancetype.Container {
		// Unregister any USB event handlers for this device.
		usbUnregisterHandler(d.inst, d.name)

		err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *usb) postStop() error {
	// Remove host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
	}

	return nil
}

// loadUsb scans the host machine for USB devices.
func (d *usb) loadUsb() ([]USBEvent, error) {
	result := []USBEvent{}

	ents, err := os.ReadDir(usbDevPath)
	if err != nil {
		/* if there are no USB devices, let's render an empty list,
		 * i.e. no usb devices */
		if os.IsNotExist(err) {
			return result, nil
		}

		return nil, err
	}

	for _, ent := range ents {
		values, err := d.loadRawValues(path.Join(usbDevPath, ent.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return []USBEvent{}, err
		}

		parts := strings.Split(values["dev"], ":")
		if len(parts) != 2 {
			return []USBEvent{}, fmt.Errorf("invalid device value %s", values["dev"])
		}

		usb, err := USBNewEvent(
			"add",
			values["idVendor"],
			values["idProduct"],
			values["serial"],
			parts[0],
			parts[1],
			values["busnum"],
			values["devnum"],
			values["devname"],
			[]string{},
			0,
		)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, err
		}

		result = append(result, usb)
	}

	return result, nil
}

func (d *usb) loadRawValues(p string) (map[string]string, error) {
	values := map[string]string{
		"idVendor":  "",
		"idProduct": "",
		"serial":    "",
		"dev":       "",
		"busnum":    "",
		"devnum":    "",
	}

	for k := range values {
		v, err := os.ReadFile(path.Join(p, k))
		if err != nil {
			if k == "serial" && os.IsNotExist(err) {
				continue
			}

			return nil, err
		}

		values[k] = strings.TrimSpace(string(v))
	}

	return values, nil
}

// getUniqueDeviceNameFromUSBEvent returns a unique device name including the bus and device number.
// Previously, the device name contained a simple incremental value as suffix. This would make the
// device unidentifiable when using hotplugging. Including the bus and device number makes the
// device identifiable.
func (d *usb) getUniqueDeviceNameFromUSBEvent(e USBEvent) string {
	return fmt.Sprintf("%s-%03d-%03d", d.name, e.BusNum, e.DevNum)
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *usb) CanHotPlug() bool {
	return true
}
