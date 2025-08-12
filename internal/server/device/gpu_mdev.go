package device

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
)

var gpuMdevMu sync.Mutex

type gpuMdev struct {
	deviceCommon
}

// Start is run when the device is added to the container.
func (d *gpuMdev) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	return d.startVM()
}

// Stop is run when the device is removed from the instance.
func (d *gpuMdev) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// startVM detects the requested GPU devices and related virtual functions and rebinds them to the vfio-pci driver.
func (d *gpuMdev) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}

	// Lock to prevent multiple concurrent mdev devices being setup.
	gpuMdevMu.Lock()
	defer gpuMdevMu.Unlock()

	// Get any existing UUID.
	v := d.volatileGet()
	mdevUUID := v["vgpu.uuid"]

	// Get the local GPUs.
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	reverter := revert.New()
	defer reverter.Fail()

	var pciAddress string
	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		if pciAddress != "" {
			return nil, errors.New("VMs cannot match multiple GPUs per device")
		}

		pciAddress = gpu.PCIAddress

		// Look for the requested mdev profile on the GPU itself.
		mdevFound := false
		mdevAvailable := false
		for k, v := range gpu.Mdev {
			if d.config["mdev"] == k {
				mdevFound = true
				if v.Available > 0 {
					mdevAvailable = true
				}

				break
			}
		}

		// If no mdev found on the GPU and SR-IOV is present, look on the VFs.
		if !mdevFound && gpu.SRIOV != nil {
			for _, vf := range gpu.SRIOV.VFs {
				for k, v := range vf.Mdev {
					if d.config["mdev"] == k {
						mdevFound = true
						if v.Available > 0 {
							mdevAvailable = true

							// Replace the PCI address with that of the VF.
							pciAddress = vf.PCIAddress
						}

						break
					}
				}

				if mdevAvailable {
					break
				}
			}
		}

		if !mdevFound {
			return nil, fmt.Errorf("Invalid mdev profile %q", d.config["mdev"])
		}

		if !mdevAvailable {
			return nil, fmt.Errorf("No available mdev for profile %q", d.config["mdev"])
		}

		// Create the vGPU.
		if mdevUUID == "" || !util.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s/%s", pciAddress, mdevUUID)) {
			mdevUUID = uuid.New().String()

			err = os.WriteFile(filepath.Join(fmt.Sprintf("/sys/bus/pci/devices/%s/mdev_supported_types/%s/create", pciAddress, d.config["mdev"])), []byte(mdevUUID), 0o200)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("The requested profile %q does not exist", d.config["mdev"])
				}

				return nil, fmt.Errorf("Failed to create virtual gpu %q: %w", mdevUUID, err)
			}

			reverter.Add(func() {
				path := fmt.Sprintf("/sys/bus/mdev/devices/%s", mdevUUID)

				if util.PathExists(path) {
					err := os.WriteFile(filepath.Join(path, "remove"), []byte("1\n"), 0o200)
					if err != nil {
						d.logger.Error("Failed to remove vgpu", logger.Ctx{"device": mdevUUID, "err": err})
					}
				}
			})
		}
	}

	if pciAddress == "" {
		return nil, errors.New("Failed to detect requested GPU device")
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, fmt.Errorf("Failed to get PCI device info for GPU %q: %w", pciAddress, err)
	}

	// Prepare the new volatile keys.
	saveData := make(map[string]string)
	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver
	saveData["vgpu.uuid"] = mdevUUID

	runConf.GPUDevice = append(runConf.GPUDevice,
		[]deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
			{Key: "vgpu", Value: mdevUUID},
		}...)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	reverter.Success()

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuMdev) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"vgpu.uuid":                "",
		})
	}()

	v := d.volatileGet()

	if v["vgpu.uuid"] != "" {
		path := fmt.Sprintf("/sys/bus/mdev/devices/%s", v["vgpu.uuid"])

		if util.PathExists(path) {
			err := os.WriteFile(filepath.Join(path, "remove"), []byte("1\n"), 0o200)
			if err != nil {
				d.logger.Error("Failed to remove vgpu", logger.Ctx{"device": v["vgpu.uuid"], "err": err})
			}
		}
	}

	return nil
}

// validateConfig checks the supplied config for correctness.
func (d *gpuMdev) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{
		// gendoc:generate(entity=devices, group=gpu_mdev, key=mdev)
		//
		// ---
		//  type: string
		//  required: yes
		//  shortdesc: The mediated device profile to use (required - for example, `i915-GVTg_V5_4`)
		"mdev",
	}

	optionalFields := []string{
		// gendoc:generate(entity=devices, group=gpu_mdev, key=vendorid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The vendor ID of the GPU device
		"vendorid",

		// gendoc:generate(entity=devices, group=gpu_mdev, key=productid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The product ID of the GPU device
		"productid",

		// gendoc:generate(entity=devices, group=gpu_mdev, key=id)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The DRM card ID of the GPU device
		"id",

		// gendoc:generate(entity=devices, group=gpu_mdev, key=pci
		//
		// ---
		//  type: strong
		//  required: no
		//  shortdesc: The PCI address of the GPU device
		"pci",
	}

	err := d.config.Validate(gpuValidationRules(requiredFields, optionalFields))
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
func (d *gpuMdev) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return errors.New("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}
