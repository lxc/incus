package pci

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/shared/util"
)

// ErrDeviceIsUSB is returned when dealing with a USB device.
var ErrDeviceIsUSB = fmt.Errorf("Device is USB instead of PCI")

// Device represents info about a PCI uevent device.
type Device struct {
	ID       string
	SlotName string
	Driver   string
}

// ParseUeventFile returns the PCI device info for a given uevent file.
func ParseUeventFile(ueventFilePath string) (Device, error) {
	dev := Device{}

	file, err := os.Open(ueventFilePath)
	if err != nil {
		return dev, err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Looking for something like this "PCI_SLOT_NAME=0000:05:10.0"
		fields := strings.SplitN(scanner.Text(), "=", 2)
		if len(fields) == 2 {
			if fields[0] == "PCI_SLOT_NAME" {
				dev.SlotName = fields[1]
			} else if fields[0] == "PCI_ID" {
				dev.ID = fields[1]
			} else if fields[0] == "DEVTYPE" && fields[1] == "usb_interface" {
				return dev, ErrDeviceIsUSB
			} else if fields[0] == "DRIVER" {
				dev.Driver = fields[1]
			}
		}
	}

	err = scanner.Err()
	if err != nil {
		return dev, err
	}

	if dev.SlotName == "" {
		return dev, fmt.Errorf("Device uevent file could not be parsed")
	}

	return dev, nil
}

// DeviceUnbind unbinds a PCI device from the OS using its PCI Slot Name.
func DeviceUnbind(pciDev Device) error {
	driverUnbindPath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver/unbind", pciDev.SlotName)
	err := os.WriteFile(driverUnbindPath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		if !os.IsNotExist(err) || !util.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s/", pciDev.SlotName)) {
			return fmt.Errorf("Failed unbinding device %q via %q: %w", pciDev.SlotName, driverUnbindPath, err)
		}
	}

	return nil
}

// DeviceSetDriverOverride registers an override driver for a PCI device using its PCI Slot Name.
func DeviceSetDriverOverride(pciDev Device, driverOverride string) error {
	overridePath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "driver_override")

	// The "\n" at end is important to allow the driver override to be cleared by passing "" in.
	err := os.WriteFile(overridePath, []byte(fmt.Sprintf("%s\n", driverOverride)), 0600)
	if err != nil {
		return fmt.Errorf("Failed setting driver override %q for device %q via %q: %w", driverOverride, pciDev.SlotName, overridePath, err)
	}

	return nil
}

// DeviceProbe probes a PCI device using its PCI Slot Name.
func DeviceProbe(pciDev Device) error {
	driveProbePath := "/sys/bus/pci/drivers_probe"
	err := os.WriteFile(driveProbePath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		return fmt.Errorf("Failed probing device %q via %q: %w", pciDev.SlotName, driveProbePath, err)
	}

	return nil
}

// DeviceDriverOverride unbinds the device, sets the driver override preference, then probes the device, and
// waits for it to be activated with the specified driver.
func DeviceDriverOverride(pciDev Device, driverOverride string) error {
	revert := revert.New()
	defer revert.Fail()

	// Unbind the device from the host (ignore if not bound).
	err := DeviceUnbind(pciDev)
	if err != nil && os.IsNotExist(err) {
		return err
	}

	revert.Add(func() {
		// Reset the driver override and rebind to original driver (if needed).
		_ = DeviceUnbind(pciDev)
		_ = DeviceSetDriverOverride(pciDev, pciDev.Driver)
		_ = DeviceProbe(pciDev)
	})

	// Set driver override.
	err = DeviceSetDriverOverride(pciDev, driverOverride)
	if err != nil {
		return err
	}

	// Probe device to bind it to overridden driver.
	err = DeviceProbe(pciDev)
	if err != nil {
		return err
	}

	vfioDev := Device{
		Driver:   driverOverride,
		SlotName: pciDev.SlotName,
	}

	// Wait for the device to be bound to the overridden driver if specified.
	if vfioDev.Driver != "" {
		err = deviceProbeWait(vfioDev)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// deviceProbeWait waits for PCI device to be activated with the specified driver after being probed.
func deviceProbeWait(pciDev Device) error {
	driverPath := fmt.Sprintf("/sys/bus/pci/drivers/%s/%s", pciDev.Driver, pciDev.SlotName)

	for i := 0; i < 10; i++ {
		if util.PathExists(driverPath) {
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("Device took too long to activate at %q", driverPath)
}

// NormaliseAddress converts common PCI address notation to the kernel's notation.
func NormaliseAddress(addr string) string {
	// PCI devices can be specified as "0000:XX:XX.X" or "XX:XX.X".
	// However, the devices in /sys/bus/pci/devices use the long format which
	// is why we need to make sure the prefix is present.
	if len(addr) == 7 {
		addr = fmt.Sprintf("0000:%s", addr)
	}

	// Ensure all addresses are lowercase.
	addr = strings.ToLower(addr)

	return addr
}

// DeviceIOMMUGroup returns the IOMMU group for a PCI device.
func DeviceIOMMUGroup(slotName string) (uint64, error) {
	iommuGroupSymPath := fmt.Sprintf("/sys/bus/pci/devices/%s/iommu_group", slotName)
	_, err := os.Lstat(iommuGroupSymPath)
	if err != nil {
		return 0, err
	}

	iommuGroupPath, err := os.Readlink(iommuGroupSymPath)
	if err != nil {
		return 0, err
	}

	iommuGroupStr := filepath.Base(iommuGroupPath)
	iommuGroup, err := strconv.ParseUint(iommuGroupStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Failed to parse %q: %w", iommuGroupStr, err)
	}

	return iommuGroup, nil
}
