package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

// Device represents an instance device.
type Device map[string]string

// Clone returns a copy of the Device.
func (device Device) Clone() Device {
	copy := make(map[string]string, len(device))

	for k, v := range device {
		copy[k] = v
	}

	return copy
}

// Validate accepts a map of field/validation functions to run against the device's config.
func (device Device) Validate(rules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(device[k])
		if err != nil {
			return fmt.Errorf("Invalid value for device option %q: %w", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range device {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// Skip type fields are these are validated by the presence of an implementation.
		if k == "type" {
			continue
		}

		// Allow user.* configuration.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		// Allow initial.* configuration.
		if strings.HasPrefix(k, "initial.") {
			continue
		}

		if k == "nictype" && (device["type"] == "nic" || device["type"] == "infiniband") {
			continue
		}

		if k == "gputype" && device["type"] == "gpu" {
			continue
		}

		return fmt.Errorf("Invalid device option %q", k)
	}

	return nil
}

// Devices represents a set of instance devices.
type Devices map[string]Device

// NewDevices creates a new Devices set from a native map[string]map[string]string set.
func NewDevices(nativeSet map[string]map[string]string) Devices {
	newDevices := Devices{}

	for devName, devConfig := range nativeSet {
		newDev := Device{}
		for k, v := range devConfig {
			if v == "" {
				continue
			}

			newDev[k] = v
		}

		newDevices[devName] = newDev
	}

	return newDevices
}

// ApplyDeviceInitialValues applies a profile initial values to root disk devices.
func ApplyDeviceInitialValues(devices Devices, profiles []api.Profile) Devices {
	for _, p := range profiles {
		for devName, devConfig := range p.Devices {
			// Apply only root disk device from profile devices to instance devices.
			if devConfig["type"] != "disk" || devConfig["path"] != "/" || devConfig["source"] != "" {
				continue
			}

			// Skip profile devices that are already present in the map of devices
			// because those devices should be already populated.
			_, ok := devices[devName]
			if ok {
				continue
			}

			// If profile device contains an initial.* key, add it to the map of devices.
			for k := range devConfig {
				if strings.HasPrefix(k, "initial.") {
					devices[devName] = devConfig
					break
				}
			}
		}
	}

	return devices
}

// Contains checks if a given device exists in the set and if it's identical to that provided.
func (list Devices) Contains(k string, d Device) bool {
	// If it didn't exist, it's different
	if list[k] == nil {
		return false
	}

	old := list[k]

	return deviceEquals(old, d)
}

// Update returns the difference between two device sets (removed, added, updated devices) and a list of all
// changed keys across all devices. Accepts a function to return which keys can be live updated, which prevents
// them being removed and re-added if the device supports live updates of certain keys.
func (list Devices) Update(newlist Devices, updateFields func(Device, Device) []string) (Devices, Devices, Devices, []string) {
	rmlist := map[string]Device{}
	addlist := map[string]Device{}
	updatelist := map[string]Device{}

	// Detect which devices have changed or been removed in in new list.
	for key, d := range list {
		// Always skip user keys.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		if !newlist.Contains(key, d) {
			rmlist[key] = d
		}
	}

	// Detect which devices have changed or been added in in new list.
	for key, d := range newlist {
		// Always skip user keys.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		if !list.Contains(key, d) {
			addlist[key] = d
		}
	}

	allChangedKeys := []string{}
	for key, d := range addlist {
		srcOldDevice := rmlist[key]
		oldDevice := srcOldDevice.Clone()

		srcNewDevice := newlist[key]
		newDevice := srcNewDevice.Clone()

		// Detect keys different between old and new device and append to the all changed keys list.
		allChangedKeys = append(allChangedKeys, deviceEqualsDiffKeys(oldDevice, newDevice)...)

		// Remove any fields that can be live-updated without adding/removing the device from instance.
		if updateFields != nil {
			for _, k := range updateFields(oldDevice, newDevice) {
				delete(oldDevice, k)
				delete(newDevice, k)
			}
		}

		// If after removing the live-updatable keys the devices are equal, then we know the device has
		// been updated rather than added or removed, so add it to the update list, and remove it from
		// the added and removed lists.
		if deviceEquals(oldDevice, newDevice) {
			delete(rmlist, key)
			delete(addlist, key)
			updatelist[key] = d
		}
	}

	return rmlist, addlist, updatelist, allChangedKeys
}

// Clone returns a copy of the Devices set.
func (list Devices) Clone() Devices {
	copy := make(Devices, len(list))

	for deviceName, device := range list {
		copy[deviceName] = device.Clone()
	}

	return copy
}

// CloneNative returns a copy of the Devices set as a native map[string]map[string]string type.
func (list Devices) CloneNative() map[string]map[string]string {
	copy := make(map[string]map[string]string, len(list))

	for deviceName, device := range list {
		copy[deviceName] = device.Clone()
	}

	return copy
}

// Sorted returns the name of all devices in the set, sorted properly.
func (list Devices) Sorted() DevicesSortable {
	sortable := DevicesSortable{}
	for k, d := range list {
		sortable = append(sortable, DeviceNamed{k, d})
	}

	sort.Sort(sortable)
	return sortable
}

// Reversed returns the name of all devices in the set, sorted reversed.
func (list Devices) Reversed() DevicesSortable {
	sortable := DevicesSortable{}
	for k, d := range list {
		sortable = append(sortable, DeviceNamed{k, d})
	}

	sort.Sort(sort.Reverse(sortable))
	return sortable
}
