//go:build linux

package resources

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/resources/usbid"
)

var (
	devSerialByPath = "/dev/serial/by-path"
	devSerialByID   = "/dev/serial/by-id"
	sysClassTty     = "/sys/class/tty"
)

// scanSerialDevice scans the directory and fill the map.
// - ID is the device name, e.g. ttyUSB0, ttyACM0.
// - DeviceID is the symlink to the device in the by-id directory.
// - DevicePath is the symlink to the device in the by-path directory.
func scanSerialDevice(deviceTable map[string]*api.ResourcesSerialDevice, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("Failed to read directory %s: %w", dir, err)
	}

	for _, e := range entries {
		symlink := filepath.Join(dir, e.Name())

		realDev, err := filepath.EvalSymlinks(symlink)
		if err != nil {
			// Skip broken udev symlink.
			continue
		}

		devName := filepath.Base(realDev)

		_, exists := deviceTable[devName]
		if !exists {
			deviceTable[devName] = &api.ResourcesSerialDevice{ID: devName}
		}

		if filepath.Base(dir) == "by-id" {
			deviceTable[devName].DeviceID = symlink
		} else {
			deviceTable[devName].DevicePath = symlink
		}
	}

	return nil
}

// enrichSerialDeviceInfo enriches the serial device info by sysfs and the USB database.
func enrichSerialDeviceInfo(deviceTable map[string]*api.ResourcesSerialDevice) error {
	// Load the USB ID database.
	usbid.Load()

	for _, dev := range deviceTable {
		sysPath := filepath.Join(sysClassTty, dev.ID)
		if !sysfsExists(sysPath) {
			return fmt.Errorf("Sysfs path does not exist: %s", sysPath)
		}

		// Get the device path.
		deviceLink := filepath.Join(sysPath, "device")
		absDevPath, err := filepath.EvalSymlinks(deviceLink)
		if err != nil {
			return fmt.Errorf("Failed to resolve device symlink %s: %w", deviceLink, err)
		}

		// Get the device major/minor.
		devFile := filepath.Join(sysPath, "dev")
		if sysfsExists(devFile) {
			data, err := os.ReadFile(devFile)
			if err != nil {
				return fmt.Errorf("Failed to read dev file %s: %w", devFile, err)
			}

			dev.Device = strings.TrimSpace(string(data))
		}

		// Get the driver.
		driverLink := filepath.Join(absDevPath, "driver")
		if sysfsExists(driverLink) {
			driverPath, err := os.Readlink(driverLink)
			if err != nil {
				return fmt.Errorf("Failed to read driver symlink %s: %w", driverLink, err)
			}

			dev.Driver = filepath.Base(driverPath)
		}

		// Get the USB vendor/product IDs and names.
		err = getSerialUSBVendorAndProduct(dev, absDevPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// getSerialUSBVendorAndProduct enriches the device USB vendor/product IDs and names.
func getSerialUSBVendorAndProduct(dev *api.ResourcesSerialDevice, absDevPath string) error {
	// Check the we are using a USB device.
	if !strings.Contains(absDevPath, "/usb") {
		return nil
	}

	// USB vendor/product IDs are in the parent's parent directory
	// e.g., if absDevPath = "/sys/devices/.../usb1/1-1/1-1:1.0/ttyUSB0"
	// then idVendor/idProduct are in "/sys/devices/.../usb1/1-1"
	usbDevicePath := filepath.Join(absDevPath, "..", "..")

	usbDevicePath, err := filepath.EvalSymlinks(usbDevicePath)
	if err != nil {
		return fmt.Errorf("Failed to resolve USB device path for %s: %w", absDevPath, err)
	}

	// Get the vendor ID.
	idVendor := filepath.Join(usbDevicePath, "idVendor")
	if sysfsExists(idVendor) {
		data, err := os.ReadFile(idVendor)
		if err != nil {
			return fmt.Errorf("Failed to read vendor ID file %s: %w", idVendor, err)
		}

		dev.VendorID = strings.TrimSpace(string(data))
	}

	// Get the product ID.
	idProduct := filepath.Join(usbDevicePath, "idProduct")
	if sysfsExists(idProduct) {
		data, err := os.ReadFile(idProduct)
		if err != nil {
			return fmt.Errorf("Failed to read product ID file %s: %w", idProduct, err)
		}

		dev.ProductID = strings.TrimSpace(string(data))
	}

	// Use usbid to get vendor and product names.
	if dev.VendorID != "" && dev.ProductID != "" {
		vendorID, err := strconv.ParseUint(strings.TrimSpace(dev.VendorID), 16, 16)
		if err != nil {
			return fmt.Errorf("Failed to parse vendor ID %q: %w", dev.VendorID, err)
		}

		vendor, exists := usbid.Vendors[usbid.ID(vendorID)]
		if exists {
			dev.Vendor = vendor.Name
			productID, err := strconv.ParseUint(strings.TrimSpace(dev.ProductID), 16, 16)
			if err != nil {
				return fmt.Errorf("Failed to parse product ID %q: %w", dev.ProductID, err)
			}

			product, exists := vendor.Product[usbid.ID(productID)]
			if exists {
				dev.Product = product.Name
			}
		}
	}

	return nil
}

// buildResourcesSerial generates the ResourcesSerial from the device table.
func buildResourcesSerial(deviceTable map[string]*api.ResourcesSerialDevice) api.ResourcesSerial {
	serial := api.ResourcesSerial{
		Devices: []api.ResourcesSerialDevice{},
	}

	for _, d := range deviceTable {
		serial.Devices = append(serial.Devices, *d)
	}

	serial.Total = uint64(len(serial.Devices))

	return serial
}

// GetSerial returns the serial devices available on the system.
func GetSerial() (*api.ResourcesSerial, error) {
	deviceTable := map[string]*api.ResourcesSerialDevice{}

	// Scan all serial devices.
	err := scanSerialDevice(deviceTable, devSerialByPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("Failed to scan serial devices by-id: %w", err)
	}

	err = scanSerialDevice(deviceTable, devSerialByID)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("Failed to scan serial devices by-path: %w", err)
	}

	// Enrich the serial device info.
	err = enrichSerialDeviceInfo(deviceTable)
	if err != nil {
		return nil, err
	}

	serial := buildResourcesSerial(deviceTable)

	return &serial, nil
}
