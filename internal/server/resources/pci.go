package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/api"
)

// GetPCI returns a filled api.ResourcesPCI struct ready for use by Incus.
func GetPCI() (*api.ResourcesPCI, error) {
	pci := api.ResourcesPCI{}
	pci.Devices = []api.ResourcesPCIDevice{}

	if !sysfsExists(sysBusPci) {
		return &pci, nil
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Get uname for driver version
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	// List all PCI devices
	entries, err := os.ReadDir(sysBusPci)
	if err != nil {
		return nil, fmt.Errorf("Failed to list %q: %w", sysBusPci, err)
	}

	for _, entry := range entries {
		entryName := entry.Name()
		devicePath := filepath.Join(sysBusPci, entryName)
		device := api.ResourcesPCIDevice{}

		// Get driver name
		driverPath := filepath.Join(devicePath, "driver")

		if sysfsExists(driverPath) {
			linkTarget, err := filepath.EvalSymlinks(driverPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to get driver of %q: %w", devicePath, err)
			}

			device.Driver = filepath.Base(linkTarget)

			// Try to get the version, fallback to kernel version
			out, err := os.ReadFile(filepath.Join(driverPath, "module", "version"))
			if err == nil {
				device.DriverVersion = strings.TrimSpace(string(out))
			} else {
				device.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
			}
		}

		// Get NUMA node
		if sysfsExists(filepath.Join(devicePath, "numa_node")) {
			numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "numa_node"), err)
			}

			if numaNode > 0 {
				device.NUMANode = uint64(numaNode)
			}
		}

		// Get PCI address
		device.PCIAddress = entryName

		// Get product ID node
		deviceDevicePath := filepath.Join(devicePath, "device")
		if sysfsExists(deviceDevicePath) {
			id, err := os.ReadFile(deviceDevicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceDevicePath, err)
			}

			device.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		// Get vendor ID node
		deviceVendorPath := filepath.Join(devicePath, "vendor")
		if sysfsExists(deviceVendorPath) {
			id, err := os.ReadFile(deviceVendorPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", deviceVendorPath, err)
			}

			device.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		// Fill vendor and product names
		if pciDB != nil {
			vendor, ok := pciDB.Vendors[device.VendorID]
			if ok {
				device.Vendor = vendor.Name

				for _, product := range vendor.Products {
					if product.ID == device.ProductID {
						device.Product = product.Name
						break
					}
				}
			}
		}

		// Get IOMMU Group
		iommuGroupSymPath := filepath.Join(sysBusPci, device.PCIAddress, "iommu_group")
		if sysfsExists(iommuGroupSymPath) {
			iommuGroupPath, err := os.Readlink(iommuGroupSymPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to readlink %q: %w", iommuGroupSymPath, err)
			}

			iommuGroup := filepath.Base(iommuGroupPath)
			device.IOMMUGroup, err = strconv.ParseUint(iommuGroup, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse %q: %w", iommuGroup, err)
			}
		} else {
			device.IOMMUGroup = 0
		}

		// Get VPD info
		vpdSysPath := filepath.Join(devicePath, "vpd")
		if sysfsExists(vpdSysPath) {
			data, err := os.ReadFile(vpdSysPath)

			// If the file is readable, parse the VPD data.
			if err == nil {
				device.VPD = parsePCIVPD(data)
			}
		}

		pci.Devices = append(pci.Devices, device)
		pci.Total++
	}

	return &pci, nil
}
