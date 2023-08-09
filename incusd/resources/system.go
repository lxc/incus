package resources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/shared/api"
)

var sysClassDMIID = "/sys/class/dmi/id"
var systemType string

// GetSystem returns a filled api.ResourcesSystem struct ready for use by LXD.
func GetSystem() (*api.ResourcesSystem, error) {
	var err error
	system := api.ResourcesSystem{}

	// Cache the system type
	if systemType == "" {
		systemType = systemGetType()
	}

	system.Type = systemType

	if !sysfsExists(sysClassDMIID) {
		return &system, nil
	}

	// Product UUID
	productUUIDPath := filepath.Join(sysClassDMIID, "product_uuid")
	if sysfsExists(productUUIDPath) {
		content, err := os.ReadFile(productUUIDPath)
		if err != nil && !os.IsPermission(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", productUUIDPath, err)
		}

		system.UUID = strings.TrimSpace(string(content))
	}

	// Vendor
	vendorPath := filepath.Join(sysClassDMIID, "sys_vendor")
	if sysfsExists(vendorPath) {
		content, err := os.ReadFile(vendorPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", vendorPath, err)
		}

		system.Vendor = strings.TrimSpace(string(content))
	}

	// Product name
	productNamePath := filepath.Join(sysClassDMIID, "product_name")
	if sysfsExists(productNamePath) {
		content, err := os.ReadFile(productNamePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", productNamePath, err)
		}

		system.Product = strings.TrimSpace(string(content))
	}

	// Product family
	productFamilyPath := filepath.Join(sysClassDMIID, "product_family")
	if sysfsExists(productFamilyPath) {
		content, err := os.ReadFile(productFamilyPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", productFamilyPath, err)
		}

		system.Family = strings.TrimSpace(string(content))
	}

	// Product version
	productVersion := filepath.Join(sysClassDMIID, "product_version")
	if sysfsExists(productVersion) {
		content, err := os.ReadFile(productVersion)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", productVersion, err)
		}

		system.Version = strings.TrimSpace(string(content))
	}

	// Product SKU
	productSKUPath := filepath.Join(sysClassDMIID, "product_sku")
	if sysfsExists(productSKUPath) {
		content, err := os.ReadFile(productSKUPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", productSKUPath, err)
		}

		system.Sku = strings.TrimSpace(string(content))
	}

	// Product serial
	productSerialPath := filepath.Join(sysClassDMIID, "product_serial")
	if sysfsExists(productSerialPath) {
		content, err := os.ReadFile(productSerialPath)
		if err != nil && !os.IsPermission(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", productSerialPath, err)
		}

		system.Serial = strings.TrimSpace(string(content))
	}

	system.Firmware, err = systemGetFirmware()
	if err != nil {
		return nil, err
	}

	system.Chassis, err = systemGetChassis()
	if err != nil {
		return nil, err
	}

	system.Motherboard, err = systemGetMotherboard()
	if err != nil {
		return nil, err
	}

	return &system, nil
}

func systemGetType() string {
	// If systemd-detect-virt is unavailable, the system type is unknown.
	_, err := exec.LookPath("systemd-detect-virt")
	if err != nil {
		return "unknown"
	}

	runDetectVirt := func(flag string) error {
		cmd := exec.Command("systemd-detect-virt", flag)
		return cmd.Run()
	}

	// If this returns 0, we're in a container.
	err = runDetectVirt("--container")
	if err == nil {
		return "container"
	}

	// If this returns 0, we're in a VM.
	err = runDetectVirt("--vm")
	if err == nil {
		return "virtual-machine"
	}

	// Since we're neither in a container nor a VM, we must be on a physical
	// machine.
	return "physical"
}

func systemGetFirmware() (*api.ResourcesSystemFirmware, error) {
	firmware := api.ResourcesSystemFirmware{}

	// Firmware vendor
	biosVendorPath := filepath.Join(sysClassDMIID, "bios_vendor")
	if sysfsExists(biosVendorPath) {
		content, err := os.ReadFile(biosVendorPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", biosVendorPath, err)
		}

		firmware.Vendor = strings.TrimSpace(string(content))
	}

	// Firmware date
	biosDatePath := filepath.Join(sysClassDMIID, "bios_date")
	if sysfsExists(biosDatePath) {
		content, err := os.ReadFile(biosDatePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", biosDatePath, err)
		}

		firmware.Date = strings.TrimSpace(string(content))
	}

	// Firmware version
	biosVersionPath := filepath.Join(sysClassDMIID, "bios_version")
	if sysfsExists(biosVersionPath) {
		content, err := os.ReadFile(biosVersionPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", biosVersionPath, err)
		}

		firmware.Version = strings.TrimSpace(string(content))
	}

	return &firmware, nil
}

func systemGetChassis() (*api.ResourcesSystemChassis, error) {
	chassis := api.ResourcesSystemChassis{}

	// Chassis vendor
	chassisVendorPath := filepath.Join(sysClassDMIID, "chassis_vendor")
	if sysfsExists(chassisVendorPath) {
		content, err := os.ReadFile(chassisVendorPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", chassisVendorPath, err)
		}

		chassis.Vendor = strings.TrimSpace(string(content))
	}

	// Chassis types according to the DMTF SMBIOS Spec
	chassisTypes := map[uint64]string{
		0x1:  "Other",
		0x2:  "Unknown",
		0x3:  "Desktop",
		0x4:  "Low Profile Desktop",
		0x5:  "Pizza Box",
		0x6:  "Mini Tower",
		0x7:  "Tower",
		0x8:  "Portable",
		0x9:  "Laptop",
		0xA:  "Notebook",
		0xB:  "Hand Held",
		0xC:  "Docking Station",
		0xD:  "All in One",
		0xE:  "Sub Notebook",
		0xF:  "Space-saving",
		0x10: "Lunch Box",
		0x11: "Main Server Chassis",
		0x12: "Expansion Chassis",
		0x13: "SubChassis",
		0x14: "Bus Expansion Chassis",
		0x15: "Peripheral Chassis",
		0x16: "RAID Chassis",
		0x17: "Rack Mount Chassis",
		0x18: "Sealed-case PC",
		0x19: "Multi-system chassis",
		0x1A: "Compact PCI",
		0x1B: "Advanced TCA",
		0x1C: "Blade",
		0x1D: "Blade Enclosure",
		0x1E: "Tablet",
		0x1F: "Convertible",
		0x20: "Detachable",
		0x21: "IoT Gateway",
		0x22: "Embedded PC",
		0x23: "Mini PC",
		0x24: "Stick PC",
	}

	// Chassis type
	chassisTypePath := filepath.Join(sysClassDMIID, "chassis_type")
	if sysfsExists(chassisTypePath) {
		chassisType, err := readUint(chassisTypePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", chassisTypePath, err)
		}

		chassis.Type = chassisTypes[chassisType]
	}

	// Chassis serial
	chassisSerialPath := filepath.Join(sysClassDMIID, "chassis_serial")
	if sysfsExists(chassisSerialPath) {
		content, err := os.ReadFile(chassisSerialPath)
		if err != nil && !os.IsPermission(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", chassisSerialPath, err)
		}

		chassis.Serial = strings.TrimSpace(string(content))
	}

	// Chassis version
	chassisVersionPath := filepath.Join(sysClassDMIID, "chassis_version")
	if sysfsExists(chassisVersionPath) {
		content, err := os.ReadFile(chassisVersionPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", chassisVersionPath, err)
		}

		chassis.Version = strings.TrimSpace(string(content))
	}

	return &chassis, nil
}

func systemGetMotherboard() (*api.ResourcesSystemMotherboard, error) {
	motherboard := api.ResourcesSystemMotherboard{}

	// Motherboard vendor name
	boardVendorPath := filepath.Join(sysClassDMIID, "board_vendor")
	if sysfsExists(boardVendorPath) {
		content, err := os.ReadFile(boardVendorPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", boardVendorPath, err)
		}

		motherboard.Vendor = strings.TrimSpace(string(content))
	}

	// Motherboard product name
	boardNamePath := filepath.Join(sysClassDMIID, "board_name")
	if sysfsExists(boardNamePath) {
		content, err := os.ReadFile(boardNamePath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", boardNamePath, err)
		}

		motherboard.Product = strings.TrimSpace(string(content))
	}

	// Motherboard serial
	boardSerialPath := filepath.Join(sysClassDMIID, "board_serial")
	if sysfsExists(boardSerialPath) {
		content, err := os.ReadFile(boardSerialPath)
		if err != nil && !os.IsPermission(err) {
			return nil, fmt.Errorf("Failed to read %q: %w", boardSerialPath, err)
		}

		motherboard.Serial = strings.TrimSpace(string(content))
	}

	// Motherboard version
	boardVersionPath := filepath.Join(sysClassDMIID, "board_version")
	if sysfsExists(boardVersionPath) {
		content, err := os.ReadFile(boardVersionPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %q: %w", boardVersionPath, err)
		}

		motherboard.Version = strings.TrimSpace(string(content))
	}

	return &motherboard, nil
}
