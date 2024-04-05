package device

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/util"
)

const gpuDRIDevPath = "/dev/dri"

// Non-card devices such as {/dev/nvidiactl, /dev/nvidia-uvm, ...}.
type nvidiaNonCardDevice struct {
	path  string
	major uint32
	minor uint32
}

type gpuPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		"vendorid",
		"productid",
		"id",
		"pci",
	}

	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		optionalFields = append(optionalFields, "uid", "gid", "mode")
	}

	err := d.config.Validate(gpuValidationRules(nil, optionalFields))
	if err != nil {
		return err
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"id", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "pci" is set`, field)
			}
		}

		d.config["pci"] = pcidev.NormaliseAddress(d.config["pci"])
	}

	if d.config["id"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "id" is set`, field)
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}

// Start is run when the device is added to the container.
func (d *gpuPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

// startContainer detects the requested GPU devices and sets up unix-char devices.
// Returns RunConfig populated with mount info required to pass the unix-char devices into the container.
func (d *gpuPhysical) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	sawNvidia := false
	found := false

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// We found a match.
		found = true

		// Setup DRM unix-char devices if present.
		if gpu.DRM != nil {
			if gpu.DRM.CardName != "" && gpu.DRM.CardDevice != "" && util.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.CardDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}

			if gpu.DRM.RenderName != "" && gpu.DRM.RenderDevice != "" && util.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.RenderDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}

			if gpu.DRM.ControlName != "" && gpu.DRM.ControlDevice != "" && util.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.ControlName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.ControlName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.ControlDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		}

		// Add Nvidia device if present.
		if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" && gpu.Nvidia.CardDevice != "" && util.PathExists(filepath.Join("/dev", gpu.Nvidia.CardName)) {
			sawNvidia = true
			path := filepath.Join("/dev", gpu.Nvidia.CardName)
			major, minor, err := d.deviceNumStringToUint32(gpu.Nvidia.CardDevice)
			if err != nil {
				return nil, err
			}

			err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup additional unix-char devices for nvidia cards.
	// No need to mount additional nvidia non-card devices as the nvidia.runtime setting will do this for us.
	if sawNvidia {
		instanceConfig := d.inst.ExpandedConfig()
		if util.IsFalseOrEmpty(instanceConfig["nvidia.runtime"]) {
			nvidiaDevices, err := d.getNvidiaNonCardDevices()
			if err != nil {
				return nil, err
			}

			for _, dev := range nvidiaDevices {
				prefix := deviceJoinPath("unix", d.name)
				if UnixDeviceExists(d.inst.DevicesPath(), prefix, dev.path) {
					continue
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, dev.major, dev.minor, dev.path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	return &runConf, nil
}

// startVM detects the requested GPU devices and related virtual functions and rebinds them to the vfio-pci driver.
func (d *gpuPhysical) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)
	var pciAddress string

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// Check for existing running processes tied to the GPU.
		// Failing early here in case of attached running processes to the card
		// avoids a blocking call to os.WriteFile() when unbinding the device.
		if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" && util.PathExists(filepath.Join("/dev", gpu.Nvidia.CardName)) {
			devPath := filepath.Join("/dev", gpu.Nvidia.CardName)
			runningProcs, err := checkAttachedRunningProcesses(devPath)
			if err != nil {
				return nil, err
			}

			if len(runningProcs) > 0 {
				return nil, fmt.Errorf(
					"Cannot use device %q, %d processes are still attached to it:\n\t%s",
					devPath, len(runningProcs), strings.Join(runningProcs, "\n\t"),
				)
			}
		}

		if pciAddress != "" {
			return nil, fmt.Errorf("VMs cannot match multiple GPUs per device")
		}

		pciAddress = gpu.PCIAddress
	}

	if pciAddress == "" {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	// Make sure that vfio-pci is loaded.
	err = linux.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, fmt.Errorf("Failed to get PCI device info for GPU %q: %w", pciAddress, err)
	}

	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver

	err = d.pciDeviceDriverOverrideIOMMU(pciDev, "vfio-pci", false)
	if err != nil {
		return nil, fmt.Errorf("Failed to override IOMMU group driver: %w", err)
	}

	runConf.GPUDevice = append(runConf.GPUDevice,
		[]deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
		}...)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// pciDeviceDriverOverrideIOMMU overrides all functions in the specified device's IOMMU group (if exists) that
// are functions of the device. If IOMMU group doesn't exist, only the device itself is overridden.
// If restore argument is true, then IOMMU VF devices related to the main device have their driver override cleared
// rather than being set to the driverOverride specified. This allows for IOMMU VFs that were using a different
// driver (or no driver) when being overridden are not restored back to the main device's driver.
func (d *gpuPhysical) pciDeviceDriverOverrideIOMMU(pciDev pcidev.Device, driverOverride string, restore bool) error {
	iommuGroupPath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "iommu_group", "devices")

	if util.PathExists(iommuGroupPath) {
		// Extract parent slot name by removing any virtual function ID.
		parts := strings.SplitN(pciDev.SlotName, ".", 2)
		prefix := parts[0]

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.
			if strings.HasPrefix(iommuSlotName, prefix) {
				iommuPciDev := pcidev.Device{
					Driver:   pciDev.Driver,
					SlotName: iommuSlotName,
				}

				if iommuSlotName != pciDev.SlotName && restore {
					// We don't know the original driver for VFs, so just remove override.
					err = pcidev.DeviceDriverOverride(iommuPciDev, "")
				} else {
					err = pcidev.DeviceDriverOverride(iommuPciDev, driverOverride)
				}

				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}
	} else {
		err := pcidev.DeviceDriverOverride(pciDev, driverOverride)
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *gpuPhysical) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if d.inst.Type() == instancetype.Container {
		err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"vgpu.uuid":                "",
		})
	}()

	v := d.volatileGet()

	if d.inst.Type() == instancetype.Container {
		// Remove host files for this device.
		err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
		if err != nil {
			return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
		}
	}

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		pciDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := d.pciDeviceDriverOverrideIOMMU(pciDev, v["last_state.pci.driver"], true)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceNumStringToUint32 converts a device number string (major:minor) into separare major and
// minor uint32s.
func (d *gpuPhysical) deviceNumStringToUint32(devNum string) (uint32, uint32, error) {
	devParts := strings.SplitN(devNum, ":", 2)
	tmp, err := strconv.ParseUint(devParts[0], 10, 32)
	if err != nil {
		return 0, 0, err
	}

	major := uint32(tmp)

	tmp, err = strconv.ParseUint(devParts[1], 10, 32)
	if err != nil {
		return 0, 0, err
	}

	minor := uint32(tmp)

	return major, minor, nil
}

// getNvidiaNonCardDevices returns device information about Nvidia non-card devices.
func (d *gpuPhysical) getNvidiaNonCardDevices() ([]nvidiaNonCardDevice, error) {
	nvidiaEnts, err := os.ReadDir("/dev")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
	}

	regexNvidiaCard, err := regexp.Compile(`^nvidia[0-9]+`)
	if err != nil {
		return nil, err
	}

	nvidiaDevices := []nvidiaNonCardDevice{}

	for _, nvidiaEnt := range nvidiaEnts {
		if !strings.HasPrefix(nvidiaEnt.Name(), "nvidia") {
			continue
		}

		// Skip the nvidia directories for now (require extra MIG support).
		if nvidiaEnt.IsDir() {
			continue
		}

		if regexNvidiaCard.MatchString(nvidiaEnt.Name()) {
			continue
		}

		nvidiaPath := filepath.Join("/dev", nvidiaEnt.Name())
		stat := unix.Stat_t{}
		err = unix.Stat(nvidiaPath, &stat)
		if err != nil {
			continue
		}

		tmpNividiaGpu := nvidiaNonCardDevice{
			path:  nvidiaPath,
			major: unix.Major(uint64(stat.Rdev)),
			minor: unix.Minor(uint64(stat.Rdev)),
		}

		nvidiaDevices = append(nvidiaDevices, tmpNividiaGpu)
	}

	return nvidiaDevices, nil
}
