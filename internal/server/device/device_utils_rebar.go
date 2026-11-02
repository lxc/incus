package device

import (
	"errors"
	"fmt"
	"strconv"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	pcidev "github.com/lxc/incus/v7/internal/server/device/pci"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/validate"
)

// rebarValidationRules adds the resizable BAR options of a PCI passthrough device to the rules.
func rebarValidationRules(rules map[string]func(string) error) {
	for bar := range 6 {
		rules[fmt.Sprintf("rebar.%d.size", bar)] = validate.Optional(validateRebarSize)
	}
}

// validateRebarSize checks that a value is a valid resizable BAR size.
func validateRebarSize(value string) error {
	size, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	if size < 1<<20 || size&(size-1) != 0 {
		return errors.New("BAR size must be a power of two of at least 1MiB")
	}

	return nil
}

// rebarApply records the current sizes of the resizable BARs of a device and applies the configured ones.
// The device is left unbound from its driver when a size had to be changed.
func rebarApply(pciDev pcidev.Device, config deviceConfig.Device, saveData map[string]string) error {
	current, err := pcidev.DeviceBARSizes(pciDev.SlotName)
	if err != nil {
		return err
	}

	for bar, size := range current {
		saveData[fmt.Sprintf("last_state.rebar.%d.size", bar)] = strconv.FormatInt(size, 10)
	}

	changes := map[int]int64{}
	for bar := range 6 {
		value := config[fmt.Sprintf("rebar.%d.size", bar)]
		if value == "" {
			continue
		}

		size, err := units.ParseByteSizeString(value)
		if err != nil {
			return err
		}

		currentSize, ok := current[bar]
		if !ok {
			return fmt.Errorf("BAR %d of device %q is not resizable", bar, pciDev.SlotName)
		}

		if currentSize != size {
			changes[bar] = size
		}
	}

	return rebarResize(pciDev, changes)
}

// rebarRestore puts the resizable BARs of a device back to the sizes recorded when it was started.
// The device is left unbound from its driver when a size had to be changed.
func rebarRestore(pciDev pcidev.Device, volatile map[string]string) error {
	current, err := pcidev.DeviceBARSizes(pciDev.SlotName)
	if err != nil {
		return err
	}

	changes := map[int]int64{}
	for bar := range 6 {
		value := volatile[fmt.Sprintf("last_state.rebar.%d.size", bar)]
		if value == "" {
			continue
		}

		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}

		currentSize, ok := current[bar]
		if ok && currentSize != size {
			changes[bar] = size
		}
	}

	return rebarResize(pciDev, changes)
}

// rebarVolatileReset adds the recorded BAR sizes to the volatile keys to clear.
func rebarVolatileReset(reset map[string]string) {
	for bar := range 6 {
		reset[fmt.Sprintf("last_state.rebar.%d.size", bar)] = ""
	}
}

// rebarResize unbinds a device from its driver and resizes the given BARs.
func rebarResize(pciDev pcidev.Device, sizes map[int]int64) error {
	if len(sizes) == 0 {
		return nil
	}

	// BARs can only be resized while no driver is bound.
	err := pcidev.DeviceUnbind(pciDev)
	if err != nil {
		return err
	}

	for bar, size := range sizes {
		err = pcidev.DeviceBARResize(pciDev.SlotName, bar, size)
		if err != nil {
			return err
		}
	}

	return nil
}
