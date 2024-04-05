package resources

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/api"
)

var devDiskByPath = "/dev/disk/by-path"
var devDiskByID = "/dev/disk/by-id"
var runUdevData = "/run/udev/data"
var sysClassBlock = "/sys/class/block"

func storageAddDriveInfo(devicePath string, disk *api.ResourcesStorageDisk) error {
	// Attempt to open the device path
	f, err := os.Open(devicePath)
	if err == nil {
		defer func() { _ = f.Close() }()

		// Retrieve the block size
		// This can't just be done with unix.Ioctl as that particular
		// return value is 32bit and stuffing it into a 64bit variable breaks on
		// big endian systems.
		var res int32
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(f.Fd()), unix.BLKPBSZGET, uintptr(unsafe.Pointer(&res)))
		if errno != 0 {
			return fmt.Errorf("Failed to BLKPBSZGET: %w", unix.Errno(errno))
		}

		disk.BlockSize = uint64(res)
	}

	// Retrieve udev information
	udevInfo := filepath.Join(runUdevData, fmt.Sprintf("b%s", disk.Device))
	if sysfsExists(udevInfo) {
		// Get the udev information
		f, err := os.Open(udevInfo)
		if err != nil {
			return fmt.Errorf("Failed to open %q: %w", udevInfo, err)
		}

		defer func() { _ = f.Close() }()

		udevProperties := map[string]string{}
		udevInfo := bufio.NewScanner(f)
		for udevInfo.Scan() {
			line := strings.TrimSpace(udevInfo.Text())

			if !strings.HasPrefix(line, "E:") {
				continue
			}

			fields := strings.SplitN(line, "=", 2)
			if len(fields) != 2 {
				continue
			}

			key := strings.TrimSpace(fields[0])
			value := strings.TrimSpace(fields[1])
			udevProperties[key] = value
		}

		// Finer grained disk type
		if udevProperties["E:ID_CDROM"] == "1" {
			disk.Type = "cdrom"
		} else if udevProperties["E:ID_USB_DRIVER"] == "usb-storage" {
			disk.Type = "usb"
		} else if udevProperties["E:ID_ATA_SATA"] == "1" {
			disk.Type = "sata"
		}

		// Firmware version (if not found in sysfs)
		if udevProperties["E:ID_REVISION"] != "" && disk.FirmwareVersion == "" {
			disk.FirmwareVersion = udevProperties["E:ID_REVISION"]
		}

		// Serial number
		serial := udevProperties["E:SCSI_IDENT_SERIAL"]
		if serial == "" {
			serial = udevProperties["E:ID_SCSI_SERIAL"]
		}

		if serial == "" {
			serial = udevProperties["E:ID_SERIAL_SHORT"]
		}

		if serial == "" {
			serial = udevProperties["E:ID_SERIAL"]
		}

		disk.Serial = serial

		// Model number (attempt to get original string from encoded value)
		if udevProperties["E:ID_MODEL_ENC"] != "" {
			model, err := udevDecode(udevProperties["E:ID_MODEL_ENC"])
			if err == nil {
				// The raw value often has padding spaces, trim them.
				disk.Model = strings.TrimSpace(model)
			} else if udevProperties["E:ID_MODEL"] != "" {
				disk.Model = udevProperties["E:ID_MODEL"]
			}
		} else if udevProperties["E:ID_MODEL"] != "" {
			disk.Model = udevProperties["E:ID_MODEL"]
		}

		// Rotation per minute
		if udevProperties["E:ID_ATA_ROTATION_RATE_RPM"] != "" && disk.RPM == 0 {
			valueUint, err := strconv.ParseUint(udevProperties["E:ID_ATA_ROTATION_RATE_RPM"], 10, 64)
			if err != nil {
				return fmt.Errorf("Failed to parse RPM value: %w", err)
			}

			disk.RPM = valueUint
		}
	}

	return nil
}

// GetStorage returns a filled api.ResourcesStorage struct ready for use by Incus.
func GetStorage() (*api.ResourcesStorage, error) {
	storage := api.ResourcesStorage{}
	storage.Disks = []api.ResourcesStorageDisk{}

	// Detect all block devices
	if sysfsExists(sysClassBlock) {
		entries, err := os.ReadDir(sysClassBlock)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysClassBlock, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassBlock, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only keep the main entries not partitions
			if !sysfsExists(devicePath) {
				continue
			}

			// Setup the entry
			disk := api.ResourcesStorageDisk{}
			disk.ID = entryName

			// Firmware revision
			if sysfsExists(filepath.Join(devicePath, "firmware_rev")) {
				firmwareRevision, err := os.ReadFile(filepath.Join(devicePath, "firmware_rev"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "firmware_rev"), err)
				}

				disk.FirmwareVersion = strings.TrimSpace(string(firmwareRevision))
			}

			// Device node
			diskDev, err := os.ReadFile(filepath.Join(entryPath, "dev"))
			if err != nil {
				if os.IsNotExist(err) {
					// This happens on multipath devices, just skip as we only care about the main node.
					continue
				}

				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "dev"), err)
			}

			disk.Device = strings.TrimSpace(string(diskDev))

			// PCI address
			pciAddr, err := pciAddress(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to find PCI address for %q: %w", devicePath, err)
			}

			if pciAddr != "" {
				disk.PCIAddress = pciAddr
			}

			// USB address
			usbAddr, err := usbAddress(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to find USB address for %q: %w", devicePath, err)
			}

			if usbAddr != "" {
				disk.USBAddress = usbAddr
			}

			// NUMA node
			if sysfsExists(filepath.Join(devicePath, "numa_node")) {
				numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "numa_node"), err)
				}

				if numaNode > 0 {
					disk.NUMANode = uint64(numaNode)
				}
			}

			// Disk model
			if sysfsExists(filepath.Join(devicePath, "model")) {
				diskModel, err := os.ReadFile(filepath.Join(devicePath, "model"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "model"), err)
				}

				disk.Model = strings.TrimSpace(string(diskModel))
			}

			// Disk type
			if sysfsExists(filepath.Join(devicePath, "subsystem")) {
				diskSubsystem, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", filepath.Join(devicePath, "subsystem"), err)
				}

				disk.Type = filepath.Base(diskSubsystem)

				if disk.Type == "rbd" {
					// Ignore rbd devices as they aren't local block devices.
					continue
				}
			}

			// Read-only
			diskRo, err := readUint(filepath.Join(entryPath, "ro"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "ro"), err)
			}

			disk.ReadOnly = diskRo == 1

			// Size
			diskSize, err := readUint(filepath.Join(entryPath, "size"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "size"), err)
			}

			disk.Size = diskSize * 512

			// Removable
			diskRemovable, err := readUint(filepath.Join(entryPath, "removable"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "removable"), err)
			}

			disk.Removable = diskRemovable == 1

			// WWN
			if sysfsExists(filepath.Join(entryPath, "wwid")) {
				diskWWN, err := os.ReadFile(filepath.Join(entryPath, "wwid"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "wwid"), err)
				}

				disk.WWN = strings.TrimSpace(string(diskWWN))
			}

			// Try to guess if dealing with CD-ROM
			if strings.HasPrefix(disk.ID, "sr") && disk.Removable {
				disk.Type = "cdrom"

				// Most cdrom drives report this as size regardless of media
				if disk.Size == 0x1fffff*512 {
					disk.Size = 0
				}
			}

			// Look for partitions
			disk.Partitions = []api.ResourcesStorageDiskPartition{}
			for _, subEntry := range entries {
				subEntryName := subEntry.Name()
				subEntryPath := filepath.Join(sysClassBlock, subEntryName)

				if !strings.HasPrefix(subEntryName, entryName) {
					continue
				}

				if !sysfsExists(filepath.Join(subEntryPath, "partition")) {
					continue
				}

				// Setup the entry
				partition := api.ResourcesStorageDiskPartition{}
				partition.ID = subEntryName

				// Parse the partition number
				partitionNumber, err := readUint(filepath.Join(subEntryPath, "partition"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(subEntryPath, "partition"), err)
				}

				partition.Partition = partitionNumber

				// Device node
				partitionDev, err := os.ReadFile(filepath.Join(subEntryPath, "dev"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(subEntryPath, "dev"), err)
				}

				partition.Device = strings.TrimSpace(string(partitionDev))

				// Read-only
				partitionRo, err := readUint(filepath.Join(subEntryPath, "ro"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(subEntryPath, "ro"), err)
				}

				partition.ReadOnly = partitionRo == 1

				// Size
				partitionSize, err := readUint(filepath.Join(subEntryPath, "size"))
				if err != nil {
					return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(subEntryPath, "size"), err)
				}

				partition.Size = partitionSize * 512

				// Add to list
				disk.Partitions = append(disk.Partitions, partition)
			}

			// Try to find the udev device path
			if sysfsExists(devDiskByPath) {
				links, err := os.ReadDir(devDiskByPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to list the links in %q: %w", devDiskByPath, err)
				}

				for _, link := range links {
					linkName := link.Name()
					linkPath := filepath.Join(devDiskByPath, linkName)

					linkTarget, err := filepath.EvalSymlinks(linkPath)
					if err != nil {
						return nil, fmt.Errorf("Failed to find %q: %w", linkPath, err)
					}

					if linkTarget == filepath.Join("/dev", entryName) {
						disk.DevicePath = linkName
					}
				}
			}

			// Try to find the udev device id
			if sysfsExists(devDiskByID) {
				links, err := os.ReadDir(devDiskByID)
				if err != nil {
					return nil, fmt.Errorf("Failed to list the links in %q: %w", devDiskByID, err)
				}

				for _, link := range links {
					linkName := link.Name()
					linkPath := filepath.Join(devDiskByID, linkName)

					linkTarget, err := filepath.EvalSymlinks(linkPath)
					if err != nil {
						return nil, fmt.Errorf("Failed to find %q: %w", linkPath, err)
					}

					if linkTarget == filepath.Join("/dev", entryName) {
						disk.DeviceID = linkName
					}
				}
			}

			// Pull direct disk information
			err = storageAddDriveInfo(filepath.Join("/dev", entryName), &disk)
			if err != nil {
				return nil, fmt.Errorf("Failed to retrieve disk information from %q: %w", filepath.Join("/dev", entryName), err)
			}

			// If no RPM set and drive is rotational, set to RPM to 1
			diskRotationalPath := filepath.Join("/sys/class/block/", entryName, "queue/rotational")
			if disk.RPM == 0 && sysfsExists(diskRotationalPath) {
				diskRotational, err := readUint(diskRotationalPath)
				if err == nil {
					disk.RPM = diskRotational
				}
			}

			// Add to list
			storage.Disks = append(storage.Disks, disk)
		}
	}

	storage.Total = 0
	for _, card := range storage.Disks {
		if storage.Disks != nil {
			storage.Total += uint64(len(card.Partitions))
		}

		storage.Total++
	}

	return &storage, nil
}
