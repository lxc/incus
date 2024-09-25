package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/usbid"
	"github.com/lxc/incus/v6/shared/api"
)

var sysBusUSB = "/sys/bus/usb/devices"

// GetUSB returns a filled api.ResourcesUSB struct ready for use by Incus.
func GetUSB() (*api.ResourcesUSB, error) {
	// Load the USB database.
	usbid.Load()

	usb := api.ResourcesUSB{}
	usb.Devices = []api.ResourcesUSBDevice{}

	if !sysfsExists(sysBusUSB) {
		return &usb, nil
	}

	// List all USB devices
	entries, err := os.ReadDir(sysBusUSB)
	if err != nil {
		return nil, fmt.Errorf("Failed to list %q: %w", sysBusUSB, err)
	}

	// Get uname for driver version
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	for _, entry := range entries {
		entryName := entry.Name()
		devicePath := filepath.Join(sysBusUSB, entryName)

		// Skip entries without a bus address
		if !sysfsExists(filepath.Join(devicePath, "busnum")) {
			continue
		}

		devClassFile := filepath.Join(devicePath, "bDeviceClass")
		if sysfsExists(devClassFile) {
			content, err := os.ReadFile(devClassFile)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", devClassFile, err)
			}

			devClass, err := strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse device class %q: %w", content, err)
			}

			// Skip USB hubs
			if devClass == 9 {
				continue
			}
		}

		device := api.ResourcesUSBDevice{}

		// Get bus address
		device.BusAddress, err = readUint(filepath.Join(devicePath, "busnum"))
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "busnum"), err)
		}

		// Get device address
		device.DeviceAddress, err = readUint(filepath.Join(devicePath, "devnum"))
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "devnum"), err)
		}

		// Get serial number
		deviceSerialPath := filepath.Join(devicePath, "iSerial")
		if sysfsExists(deviceSerialPath) {
			content, err := os.ReadFile(deviceSerialPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceSerialPath, err)
			}

			device.Serial = strings.TrimSpace(string(content))
		}

		// Get product ID
		var productID uint64

		deviceProductIDPath := filepath.Join(devicePath, "idProduct")
		if sysfsExists(deviceProductIDPath) {
			content, err := os.ReadFile(deviceProductIDPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceProductIDPath, err)
			}

			device.ProductID = strings.TrimPrefix(strings.TrimSpace(string(content)), "0x")

			productID, err = strconv.ParseUint(device.ProductID, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse product ID %q: %w", device.ProductID, err)
			}
		}

		// Get vendor ID
		var vendorID uint64

		deviceVendorIDPath := filepath.Join(devicePath, "idVendor")
		if sysfsExists(deviceVendorIDPath) {
			content, err := os.ReadFile(deviceVendorIDPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceVendorIDPath, err)
			}

			device.VendorID = strings.TrimPrefix(strings.TrimSpace(string(content)), "0x")

			vendorID, err = strconv.ParseUint(device.VendorID, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse vendor ID %q: %w", device.VendorID, err)
			}
		}

		// Get vendor and product name
		deviceProductPath := filepath.Join(devicePath, "product")
		if sysfsExists(deviceProductPath) {
			content, err := os.ReadFile(deviceProductPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceProductPath, err)
			}

			device.Product = strings.TrimSpace(string(content))
		}

		vendor := usbid.Vendors[usbid.ID(vendorID)]
		if vendor != nil {
			device.Vendor = vendor.Name

			// If there's no product file, get it from usbid.
			if device.Product == "" {
				product := vendor.Product[usbid.ID(productID)]
				if product != nil {
					device.Product = product.Name
				}
			}
		}

		// Get speed
		deviceSpeedPath := filepath.Join(devicePath, "speed")
		if sysfsExists(deviceSpeedPath) {
			content, err := os.ReadFile(deviceSpeedPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceSpeedPath, err)
			}

			device.Speed, err = strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse speed %q: %w", content, err)
			}
		}

		// List USB interfaces
		subEntries, err := os.ReadDir(devicePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", devicePath, err)
		}

		for _, subEntry := range subEntries {
			subEntryName := subEntry.Name()
			subDevicePath := filepath.Join(devicePath, subEntryName)

			// Skip irrelevant directories and file entries
			if !subEntry.IsDir() || !strings.HasPrefix(subEntryName, entryName) {
				continue
			}

			iface := api.ResourcesUSBDeviceInterface{}

			// Get class ID
			var class *usbid.Class

			interfaceClassPath := filepath.Join(subDevicePath, "bInterfaceClass")
			if sysfsExists(interfaceClassPath) {
				content, err := os.ReadFile(interfaceClassPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", interfaceClassPath, err)
				}

				iface.ClassID, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed to parse class ID %q: %w", content, err)
				}

				var ok bool

				class, ok = usbid.Classes[usbid.ClassCode(iface.ClassID)]
				if ok {
					iface.Class = class.Name
				}
			}

			// Get subclass ID
			interfaceSubClassPath := filepath.Join(subDevicePath, "bInterfaceSubClass")
			if sysfsExists(interfaceSubClassPath) {
				content, err := os.ReadFile(interfaceSubClassPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", interfaceSubClassPath, err)
				}

				iface.SubClassID, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed to parse subclass ID %q: %w", content, err)
				}

				if iface.SubClassID > 0 && class != nil {
					subclass, ok := class.SubClass[usbid.ClassCode(iface.SubClassID)]
					if ok {
						iface.SubClass = subclass.Name
					}
				}
			}

			// Get number
			interfaceNumber := filepath.Join(subDevicePath, "bInterfaceNumber")
			if sysfsExists(interfaceNumber) {
				content, err := os.ReadFile(interfaceNumber)
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", interfaceNumber, err)
				}

				iface.Number, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed to parse interface number %q: %w", content, err)
				}
			}

			// Get driver
			driverPath := filepath.Join(subDevicePath, "driver")
			if sysfsExists(driverPath) {
				linkTarget, err := filepath.EvalSymlinks(driverPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to get driver of %q: %w", subDevicePath, err)
				}

				iface.Driver = filepath.Base(linkTarget)

				// Try to get the version, fallback to kernel version
				out, err := os.ReadFile(filepath.Join(driverPath, "module", "version"))
				if err == nil {
					iface.DriverVersion = strings.TrimSpace(string(out))
				} else {
					iface.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
				}
			}

			device.Interfaces = append(device.Interfaces, iface)
		}

		usb.Devices = append(usb.Devices, device)
		usb.Total++
	}

	return &usb, nil
}
