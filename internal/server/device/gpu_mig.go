package device

import (
	"errors"
	"fmt"
	"strings"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/util"
)

type gpuMIG struct {
	deviceCommon
}

// GPUNvidiaDeviceKey is the key used for NVIDIA devices through libnvidia-container.
const GPUNvidiaDeviceKey = "nvidia.device"

// validateConfig checks the supplied config for correctness.
func (d *gpuMIG) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}

	optionalFields := []string{
		// gendoc:generate(entity=devices, group=gpu_mig, key=vendorid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The vendor ID of the GPU device
		"vendorid",

		// gendoc:generate(entity=devices, group=gpu_mig, key=productid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The product ID of the GPU device
		"productid",

		// gendoc:generate(entity=devices, group=gpu_mig, key=id)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The DRM card ID of the GPU device
		"id",

		// gendoc:generate(entity=devices, group=gpu_mig, key=pci)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The PCI address of the GPU device
		"pci",

		// gendoc:generate(entity=devices, group=gpu_mig, key=mig.gi)
		//
		// ---
		//  type: int
		//  required: no
		//  shortdesc: Existing MIG GPU instance ID
		"mig.gi",

		// gendoc:generate(entity=devices, group=gpu_mig, key=mig.ci)
		//
		// ---
		//  type: int
		//  required: no
		//  shortdesc: Existing MIG compute instance ID
		"mig.ci",

		// gendoc:generate(entity=devices, group=gpu_mig, key=mig.uuid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: Existing MIG device UUID (MIG- prefix can be omitted)
		"mig.uuid",
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

	if d.config["mig.uuid"] != "" {
		for _, field := range []string{"mig.gi", "mig.ci"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "mig.uuid" is set`, field)
			}
		}
	} else if d.config["mig.gi"] == "" || d.config["mig.ci"] == "" {
		return errors.New(`Either "mig.uuid" or both "mig.gi" and "mig.ci" must be set`)
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuMIG) validateEnvironment() error {
	if util.IsFalseOrEmpty(d.inst.ExpandedConfig()["nvidia.runtime"]) {
		return errors.New("nvidia.runtime must be set to true for MIG GPUs to work")
	}

	return validatePCIDevice(d.config["pci"])
}

// buildMIGDeviceName builds the name of the MIG device based on old/new format.
func (d *gpuMIG) buildMIGDeviceName(gpu api.ResourcesGPUCard) string {
	if d.config["mig.uuid"] != "" {
		if strings.HasPrefix(d.config["mig.uuid"], "MIG-") {
			return d.config["mig.uuid"]
		}

		return fmt.Sprintf("MIG-%s", d.config["mig.uuid"])
	}

	return fmt.Sprintf("MIG-%s/%s/%s", gpu.Nvidia.UUID, d.config["mig.gi"], d.config["mig.ci"])
}

// CanHotPlug returns whether the device can be managed whilst the instance is running,.
func (d *gpuMIG) CanHotPlug() bool {
	return false
}

// Start is run when the device is added to the container.
func (d *gpuMIG) Start() (*deviceConfig.RunConfig, error) {
	// Check the basic config.
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}

	// Get all the GPUs.
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	var pciAddress string
	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// We found a match.
		if pciAddress != "" {
			return nil, errors.New("More than one GPU matched the MIG device")
		}

		pciAddress = gpu.PCIAddress

		// Validate the GPU.
		if gpu.Nvidia == nil {
			return nil, errors.New("Card isn't a NVIDIA GPU or driver isn't properly setup")
		}

		// Validate the MIG.
		fields := strings.SplitN(gpu.Nvidia.CardDevice, ":", 2)
		if len(fields) != 2 {
			return nil, errors.New("Bad NVIDIA GPU (couldn't find ID)")
		}

		gpuID := fields[1]

		if d.config["mig.uuid"] == "" {
			if !util.PathExists(fmt.Sprintf("/proc/driver/nvidia/capabilities/gpu%s/mig/gi%s/ci%s/access", gpuID, d.config["mig.gi"], d.config["mig.ci"])) {
				return nil, fmt.Errorf("MIG device gi=%s ci=%s doesn't exist on GPU %s", d.config["mig.gi"], d.config["mig.ci"], gpuID)
			}
		}

		runConf.GPUDevice = append(runConf.GPUDevice, []deviceConfig.RunConfigItem{
			{Key: GPUNvidiaDeviceKey, Value: d.buildMIGDeviceName(gpu)},
		}...)
	}

	if pciAddress == "" {
		return nil, errors.New("Failed to detect requested GPU device")
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *gpuMIG) Stop() (*deviceConfig.RunConfig, error) {
	return nil, nil
}
