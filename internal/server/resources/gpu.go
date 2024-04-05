package resources

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/api"
)

var sysClassDrm = "/sys/class/drm"
var procDriverNvidia = "/proc/driver/nvidia"

func loadNvidiaProc() (map[string]*api.ResourcesGPUCardNvidia, error) {
	nvidiaCards := map[string]*api.ResourcesGPUCardNvidia{}

	gpusPath := filepath.Join(procDriverNvidia, "gpus")
	if !sysfsExists(gpusPath) {
		return nil, fmt.Errorf("No NVIDIA GPU proc driver")
	}

	// List the GPUs from /proc
	entries, err := os.ReadDir(gpusPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to list %q: %w", gpusPath, err)
	}

	for _, entry := range entries {
		entryName := entry.Name()
		entryPath := filepath.Join(gpusPath, entryName)

		if !sysfsExists(filepath.Join(entryPath, "information")) {
			continue
		}

		// Get the GPU information
		f, err := os.Open(filepath.Join(entryPath, "information"))
		if err != nil {
			return nil, fmt.Errorf("Failed to open %q: %w", filepath.Join(entryPath, "information"), err)
		}

		defer func() { _ = f.Close() }()

		gpuInfo := bufio.NewScanner(f)
		nvidiaCard := &api.ResourcesGPUCardNvidia{}
		for gpuInfo.Scan() {
			line := strings.TrimSpace(gpuInfo.Text())

			fields := strings.SplitN(line, ":", 2)
			if len(fields) != 2 {
				continue
			}

			key := strings.TrimSpace(fields[0])
			value := strings.TrimSpace(fields[1])

			if key == "Model" {
				nvidiaCard.Model = value
				nvidiaCard.Brand = strings.Split(value, " ")[0]
			}

			if key == "Device Minor" {
				nvidiaCard.CardName = fmt.Sprintf("nvidia%s", value)
				nvidiaCard.CardDevice = fmt.Sprintf("195:%s", value)
			}
		}

		nvidiaCards[entryName] = nvidiaCard
	}

	return nvidiaCards, nil
}

func loadNvidiaContainer() (map[string]*api.ResourcesGPUCardNvidia, error) {
	// Check for nvidia-container-cli
	_, err := exec.LookPath("nvidia-container-cli")
	if err != nil {
		return nil, fmt.Errorf("Failed to locate nvidia-container-cli: %w", err)
	}

	// Prepare nvidia-container-cli call
	cmd := exec.Command("nvidia-container-cli", "info", "--csv")
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("Failed to setup PIPE for nvidia-container-cli: %w", err)
	}

	// Run the command
	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("Failed to start nvidia-container-cli: %w", err)
	}

	// Parse the data
	r := csv.NewReader(outPipe)
	r.FieldsPerRecord = -1

	nvidiaCards := map[string]*api.ResourcesGPUCardNvidia{}
	nvidiaNVRM := ""
	nvidiaCUDA := ""

	line := 0
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}

		line++

		if err != nil {
			continue
		}

		if line == 2 && len(record) >= 2 {
			nvidiaNVRM = record[0]
			nvidiaCUDA = record[1]
		} else if line >= 4 {
			nvidiaCards[record[5]] = &api.ResourcesGPUCardNvidia{
				NVRMVersion:  nvidiaNVRM,
				CUDAVersion:  nvidiaCUDA,
				Brand:        record[3],
				Model:        record[2],
				UUID:         record[4],
				Architecture: record[6],
				CardName:     fmt.Sprintf("nvidia%s", record[1]),
				CardDevice:   fmt.Sprintf("195:%s", record[1]),
			}
		}
	}

	// wait for nvidia-container-cli
	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("nvidia-container-cli failed: %w", err)
	}

	return nvidiaCards, nil
}

func gpuAddDeviceInfo(devicePath string, nvidiaCards map[string]*api.ResourcesGPUCardNvidia, pciDB *pcidb.PCIDB, uname unix.Utsname, card *api.ResourcesGPUCard) error {
	// Handle nested devices.
	if isDir(filepath.Join(devicePath, "device")) {
		return gpuAddDeviceInfo(filepath.Join(devicePath, "device"), nvidiaCards, pciDB, uname, card)
	}

	// SRIOV
	if sysfsExists(filepath.Join(devicePath, "sriov_numvfs")) {
		sriov := api.ResourcesGPUCardSRIOV{}

		// Get maximum and current VF count
		vfMaximum, err := readUint(filepath.Join(devicePath, "sriov_totalvfs"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "sriov_totalvfs"), err)
		}

		vfCurrent, err := readUint(filepath.Join(devicePath, "sriov_numvfs"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "sriov_numvfs"), err)
		}

		sriov.MaximumVFs = vfMaximum
		sriov.CurrentVFs = vfCurrent

		// Add the SRIOV data to the card
		card.SRIOV = &sriov
	}

	// NUMA node
	if sysfsExists(filepath.Join(devicePath, "numa_node")) {
		numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "numa_node"), err)
		}

		if numaNode > 0 {
			card.NUMANode = uint64(numaNode)
		}
	}

	deviceUSBPath := filepath.Join(devicePath, "device", "busnum")
	if sysfsExists(deviceUSBPath) {
		// USB address
		deviceDevicePath := filepath.Join(devicePath, "device")
		usbAddr, err := usbAddress(deviceDevicePath)
		if err != nil {
			return fmt.Errorf("Failed to find USB address for %q: %w", devicePath, err)
		}

		if usbAddr != "" {
			card.USBAddress = usbAddr
		}
	} else {
		// Vendor and product
		deviceVendorPath := filepath.Join(devicePath, "vendor")
		if sysfsExists(deviceVendorPath) {
			id, err := os.ReadFile(deviceVendorPath)
			if err != nil {
				return fmt.Errorf("Failed to read %q: %w", deviceVendorPath, err)
			}

			card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		deviceDevicePath := filepath.Join(devicePath, "device")
		if sysfsExists(deviceDevicePath) {
			id, err := os.ReadFile(deviceDevicePath)
			if err != nil {
				return fmt.Errorf("Failed to read %q: %w", deviceDevicePath, err)
			}

			card.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		// Fill vendor and product names
		if pciDB != nil {
			vendor, ok := pciDB.Vendors[card.VendorID]
			if ok {
				card.Vendor = vendor.Name

				for _, product := range vendor.Products {
					if product.ID == card.ProductID {
						card.Product = product.Name
						break
					}
				}
			}
		}
	}

	// Driver information
	driverPath := filepath.Join(devicePath, "driver")
	if sysfsExists(driverPath) {
		linkTarget, err := filepath.EvalSymlinks(driverPath)
		if err != nil {
			return fmt.Errorf("Failed to find %q: %w", driverPath, err)
		}

		// Set the driver name
		card.Driver = filepath.Base(linkTarget)

		// Try to get the version, fallback to kernel version
		out, err := os.ReadFile(filepath.Join(driverPath, "module", "version"))
		if err == nil {
			card.DriverVersion = strings.TrimSpace(string(out))
		} else {
			card.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
		}
	}

	// NVIDIA specific stuff
	if card.Driver == "nvidia" && card.PCIAddress != "" {
		nvidia, ok := nvidiaCards[card.PCIAddress]
		if ok {
			card.Nvidia = nvidia
		} else {
			nvidia, ok := nvidiaCards[fmt.Sprintf("0000%s", card.PCIAddress)]
			if ok {
				card.Nvidia = nvidia
			}
		}
	}

	// DRM information
	drmPath := filepath.Join(devicePath, "drm")
	if sysfsExists(drmPath) {
		drm := api.ResourcesGPUCardDRM{}

		// List all the devices
		entries, err := os.ReadDir(drmPath)
		if err != nil {
			return fmt.Errorf("Failed to list %q: %w", drmPath, err)
		}

		// Fill in the struct
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(drmPath, entryName)

			if strings.HasPrefix(entryName, "card") {
				// Get the card ID
				idStr := strings.TrimPrefix(entryName, "card")
				id, err := strconv.ParseUint(idStr, 10, 64)
				if err != nil {
					return fmt.Errorf("Failed to parse card number: %w", err)
				}

				dev, err := os.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "dev"), err)
				}

				drm.ID = id
				drm.CardName = entryName
				drm.CardDevice = strings.TrimSpace(string(dev))
			}

			if strings.HasPrefix(entryName, "controlD") {
				dev, err := os.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "dev"), err)
				}

				drm.ControlName = entryName
				drm.ControlDevice = strings.TrimSpace(string(dev))
			}

			if strings.HasPrefix(entryName, "renderD") {
				dev, err := os.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(entryPath, "dev"), err)
				}

				drm.RenderName = entryName
				drm.RenderDevice = strings.TrimSpace(string(dev))
			}
		}

		card.DRM = &drm
	}

	// DRM information
	mdevPath := filepath.Join(devicePath, "mdev_supported_types")
	if sysfsExists(mdevPath) {
		card.Mdev = map[string]api.ResourcesGPUCardMdev{}

		// List all the devices
		entries, err := os.ReadDir(mdevPath)
		if err != nil {
			return fmt.Errorf("Failed to list %q: %w", mdevPath, err)
		}

		// Fill in the struct
		for _, entry := range entries {
			mdev := api.ResourcesGPUCardMdev{}
			entryName := entry.Name()
			entryPath := filepath.Join(mdevPath, entryName)

			// API
			apiPath := filepath.Join(entryPath, "device_api")
			if sysfsExists(apiPath) {
				api, err := os.ReadFile(apiPath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", apiPath, err)
				}

				mdev.API = strings.TrimSpace(string(api))
			}

			// Available
			availablePath := filepath.Join(entryPath, "available_instances")
			if sysfsExists(availablePath) {
				available, err := readUint(availablePath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", availablePath, err)
				}

				mdev.Available = available
			}

			// Description
			descriptionPath := filepath.Join(entryPath, "description")
			if sysfsExists(descriptionPath) {
				description, err := os.ReadFile(descriptionPath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", descriptionPath, err)
				}

				mdev.Description = strings.TrimSpace(string(description))
			}

			// Devices
			mdevDevicesPath := filepath.Join(entryPath, "devices")
			if sysfsExists(mdevDevicesPath) {
				devs, err := os.ReadDir(mdevDevicesPath)
				if err != nil {
					return fmt.Errorf("Failed to list %q: %w", mdevDevicesPath, err)
				}

				mdev.Devices = []string{}
				for _, dev := range devs {
					mdev.Devices = append(mdev.Devices, dev.Name())
				}
			}

			// Name
			namePath := filepath.Join(entryPath, "name")
			if sysfsExists(namePath) {
				name, err := os.ReadFile(namePath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", namePath, err)
				}

				mdev.Name = strings.TrimSpace(string(name))
			}

			card.Mdev[entryName] = mdev
		}
	}

	return nil
}

// GetGPU returns a filled api.ResourcesGPU struct ready for use by Incus.
func GetGPU() (*api.ResourcesGPU, error) {
	gpu := api.ResourcesGPU{}
	gpu.Cards = []api.ResourcesGPUCard{}

	// Get uname for driver version
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Load NVIDIA information
	nvidiaCards, err := loadNvidiaContainer()
	if err != nil {
		nvidiaCards, err = loadNvidiaProc()
		if err != nil {
			nvidiaCards = map[string]*api.ResourcesGPUCardNvidia{}
		}
	}

	// Temporary variables
	pciKnown := []string{}
	pciVFs := map[string][]api.ResourcesGPUCard{}

	// Detect all GPUs available through kernel drm interface
	if sysfsExists(sysClassDrm) {
		entries, err := os.ReadDir(sysClassDrm)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysClassDrm, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassDrm, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only care about cards not renderers
			if !strings.HasPrefix(entryName, "card") {
				continue
			}

			// Only keep the main entries not sub-cards
			if !sysfsExists(filepath.Join(entryPath, "dev")) {
				continue
			}

			// Setup the entry
			card := api.ResourcesGPUCard{}

			// PCI address.
			pciAddr, err := pciAddress(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to find PCI address for %q: %w", devicePath, err)
			}

			if pciAddr != "" {
				card.PCIAddress = pciAddr

				// Skip devices we already know about
				if slices.Contains(pciKnown, card.PCIAddress) {
					continue
				}

				pciKnown = append(pciKnown, card.PCIAddress)
			}

			// Add device information
			err = gpuAddDeviceInfo(devicePath, nvidiaCards, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", filepath.Join(devicePath, "physfn"), err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesGPUCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				gpu.Cards = append(gpu.Cards, card)
			}
		}
	}

	// Detect remaining GPUs on PCI bus
	if sysfsExists(sysBusPci) {
		entries, err := os.ReadDir(sysBusPci)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysBusPci, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			devicePath := filepath.Join(sysBusPci, entryName)

			// Skip devices we already know about
			if slices.Contains(pciKnown, entryName) {
				continue
			}

			// Only care about identifiable devices
			if !sysfsExists(filepath.Join(devicePath, "class")) {
				continue
			}

			class, err := os.ReadFile(filepath.Join(devicePath, "class"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "class"), err)
			}

			// Only care about VGA devices
			if !strings.HasPrefix(string(class), "0x03") {
				continue
			}

			// Start building up data
			card := api.ResourcesGPUCard{}
			card.PCIAddress = entryName

			// Add device information
			err = gpuAddDeviceInfo(devicePath, nvidiaCards, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", filepath.Join(devicePath, "physfn"), err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesGPUCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				gpu.Cards = append(gpu.Cards, card)
			}
		}
	}

	// Add SRIOV devices and count devices
	gpu.Total = 0
	for _, card := range gpu.Cards {
		if card.SRIOV != nil {
			card.SRIOV.VFs = pciVFs[card.PCIAddress]
			gpu.Total += uint64(len(card.SRIOV.VFs))
		}

		gpu.Total++
	}

	return &gpu, nil
}
