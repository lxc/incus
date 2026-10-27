package device

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/lxc/incus/v7/internal/linux"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	pcidev "github.com/lxc/incus/v7/internal/server/device/pci"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/shared/resources"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

// gpuNativeContextDefaultBlobSize is the default size of the host-visible blob window
// (QEMU "hostmem") reserved for the virtio-gpu device when "blob.size" is not set.
const gpuNativeContextDefaultBlobSize = "2GiB"

type gpuNativeContext struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuNativeContext) validateConfig(instConf instance.ConfigReader, partialValidation bool) error {
	// virtio-gpu DRM native context is only meaningful for VMs (host GPU acceleration
	// is provided to the guest through QEMU's virtio-gpu device, not into a container).
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		// gendoc:generate(entity=devices, group=gpu_native_context, key=vendorid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The vendor ID of the GPU device
		"vendorid",

		// gendoc:generate(entity=devices, group=gpu_native_context, key=productid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The product ID of the GPU device
		"productid",

		// gendoc:generate(entity=devices, group=gpu_native_context, key=id)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The DRM card ID of the GPU device
		"id",

		// gendoc:generate(entity=devices, group=gpu_native_context, key=pci)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The PCI address of the GPU device
		"pci",

		// gendoc:generate(entity=devices, group=gpu_native_context, key=blob.size)
		//
		// ---
		//  type: string
		//  default: 2GiB
		//  required: no
		//  shortdesc: Size of the host-visible blob memory window for the `virtio-gpu` device
		"blob.size",
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
func (d *gpuNativeContext) validateEnvironment() error {
	if d.inst.Type() != instancetype.VM {
		return ErrUnsupportedDevType
	}

	if util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return errors.New("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}

// Start is run when the device is added to the instance.
func (d *gpuNativeContext) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	return d.startVM()
}

// startVM resolves the (optionally) selected GPU to its DRM render node and returns
// the runtime configuration consumed by the QEMU driver. Unlike physical passthrough
// it does not rebind anything to vfio-pci; the host GPU stays owned by the host and is
// shared with the guest through QEMU's virtio-gpu DRM native context.
func (d *gpuNativeContext) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}

	// Render node path to hand to QEMU's egl-headless display.
	rendernode := ""

	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	// Only match on selectors when the user actually selected a card. Any of the
	// standard GPU selector keys may be used to pick which host GPU to accelerate with.
	if d.config["id"] != "" || d.config["pci"] != "" || d.config["vendorid"] != "" || d.config["productid"] != "" {
		for _, gpu := range gpus.Cards {
			// Skip any cards that are not selected.
			if !gpuSelected(d.Config(), gpu) {
				continue
			}

			if rendernode != "" {
				return nil, errors.New("VMs cannot match multiple GPUs per device")
			}

			if gpu.DRM == nil || gpu.DRM.RenderName == "" {
				return nil, fmt.Errorf("Selected GPU %q has no DRM render node", gpu.PCIAddress)
			}

			rendernode = filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)
		}

		if rendernode == "" {
			return nil, errors.New("Failed to detect requested GPU device")
		}
	} else {
		// With no card selected, default to the host GPU with the lowest render node
		// so the node is known ahead of time and can be made accessible to QEMU.
		for _, gpu := range gpus.Cards {
			if gpu.DRM == nil || gpu.DRM.RenderName == "" {
				continue
			}

			node := filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)
			if rendernode == "" || node < rendernode {
				rendernode = node
			}
		}

		if rendernode == "" {
			return nil, errors.New("Failed to find a GPU with a DRM render node")
		}
	}

	// QEMU runs as an unprivileged user and virglrenderer only opens the render node
	// once the guest starts using the GPU, well after privileges were dropped. Grant
	// that user access through a POSIX ACL on the node. The entry is left in place on
	// stop: it only covers the Incus unprivileged user and AppArmor keeps VMs without
	// a native context GPU away from the node.
	if d.state.OS.UnprivUser != "" {
		err = linux.GrantPosixACLUser(rendernode, d.state.OS.UnprivUID, 0o6)
		if err != nil {
			return nil, fmt.Errorf("Failed to grant %q access to %q: %w", d.state.OS.UnprivUser, rendernode, err)
		}
	}

	// Resolve the host-visible blob window size, defaulting when unset. It is passed to
	// the QEMU driver as a byte count for the device's "hostmem" property.
	blobSize := d.config["blob.size"]
	if blobSize == "" {
		blobSize = gpuNativeContextDefaultBlobSize
	}

	blobSizeBytes, err := units.ParseByteSizeString(blobSize)
	if err != nil {
		return nil, fmt.Errorf("Invalid blob.size %q: %w", blobSize, err)
	}

	runConf.GPUDevice = append(runConf.GPUDevice,
		[]deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "gpuType", Value: "native-context"},
			{Key: "rendernode", Value: rendernode},
			{Key: "hostmem", Value: fmt.Sprintf("%d", blobSizeBytes)},
		}...)

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *gpuNativeContext) Stop() (*deviceConfig.RunConfig, error) {
	return &deviceConfig.RunConfig{}, nil
}
