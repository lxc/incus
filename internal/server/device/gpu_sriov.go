package device

import (
	"fmt"
	"slices"
	"sync"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/revert"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/util"
)

// sriovMu is used to lock concurrent GPU allocations.
var sriovMu sync.Mutex

type gpuSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuSRIOV) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}

	optionalFields := []string{
		"vendorid",
		"productid",
		"id",
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
func (d *gpuSRIOV) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}

// Start is run when the device is added to the instance.
func (d *gpuSRIOV) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	saveData := make(map[string]string)

	// Make sure that vfio-pci is loaded.
	err = linux.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Get global SR-IOV lock to prevent concurent allocations of the VF.
	sriovMu.Lock()
	defer sriovMu.Unlock()

	// Get SRIOV VF.
	parentPCIAddress, vfID, err := d.getVF()
	if err != nil {
		return nil, err
	}

	vfPCIDev, err := d.setupSriovParent(parentPCIAddress, vfID, saveData)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.GPUDevice = append(runConf.GPUDevice, []deviceConfig.RunConfigItem{
		{Key: "devName", Value: d.name},
		{Key: "pciSlotName", Value: vfPCIDev.SlotName},
	}...)

	return &runConf, nil
}

// getVF returns the parent PCI address and VF id for a matching GPU.
func (d *gpuSRIOV) getVF() (string, int, error) {
	// List all the GPUs.
	gpus, err := resources.GetGPU()
	if err != nil {
		return "", -1, err
	}

	// If NUMA restricted, build up a list of nodes.
	var numaNodeSet []int64
	var numaNodeSetFallback []int64

	numaNodes := d.inst.ExpandedConfig()["limits.cpu.nodes"]
	if numaNodes != "" {
		if numaNodes == "balanced" {
			numaNodes = d.inst.ExpandedConfig()["volatile.cpu.nodes"]
		}

		// Parse the NUMA restriction.
		numaNodeSet, err = resources.ParseNumaNodeSet(numaNodes)
		if err != nil {
			return "", -1, err
		}

		// List all the CPUs.
		cpus, err := resources.GetCPU()
		if err != nil {
			return "", -1, err
		}

		// Get list of socket IDs from the list of NUMA nodes.
		numaSockets := make([]uint64, 0, len(cpus.Sockets))

		for _, cpuSocket := range cpus.Sockets {
			if slices.Contains(numaSockets, cpuSocket.Socket) {
				continue
			}

			for _, cpuCore := range cpuSocket.Cores {
				found := false
				for _, cpuThread := range cpuCore.Threads {
					if slices.Contains(numaNodeSet, int64(cpuThread.NUMANode)) {
						numaSockets = append(numaSockets, cpuSocket.Socket)
						found = true
						break
					}
				}

				if found {
					break
				}
			}
		}

		// Get the list of NUMA nodes from the socket list.
		numaNodeSetFallback = []int64{}

		for _, cpuSocket := range cpus.Sockets {
			if !slices.Contains(numaSockets, cpuSocket.Socket) {
				continue
			}

			for _, cpuCore := range cpuSocket.Cores {
				for _, cpuThread := range cpuCore.Threads {
					if !slices.Contains(numaNodeSetFallback, int64(cpuThread.NUMANode)) {
						numaNodeSetFallback = append(numaNodeSetFallback, int64(cpuThread.NUMANode))
					}
				}
			}
		}
	}

	// Locate a suitable VF from the least loaded suitable card.
	var pciAddress string
	var vfID int
	var cardTotal int
	var cardAvailable int
	cardNUMA := -1

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// Skip any card without SR-IOV.
		if gpu.SRIOV == nil {
			continue
		}

		// Find available VFs.
		vfs := []int{}

		for id, vf := range gpu.SRIOV.VFs {
			if vf.Driver == "" {
				vfs = append(vfs, id)
			}
		}

		// Skip if no available VFs.
		if len(vfs) == 0 {
			continue
		}

		// Handle NUMA.
		if numaNodeSet != nil {
			// Switch to current card if it matches our main NUMA node and existing card doesn't.
			if !slices.Contains(numaNodeSet, int64(cardNUMA)) && slices.Contains(numaNodeSet, int64(gpu.NUMANode)) {
				pciAddress = gpu.PCIAddress
				vfID = vfs[0]
				cardAvailable = len(vfs)
				cardTotal = int(gpu.SRIOV.CurrentVFs)
				cardNUMA = int(gpu.NUMANode)

				continue
			}

			// Skip current card if we already have a card matching our main NUMA node and this card doesn't.
			if slices.Contains(numaNodeSet, int64(cardNUMA)) && !slices.Contains(numaNodeSet, int64(gpu.NUMANode)) {
				continue
			}

			// Switch to current card if it matches a fallback NUMA node and existing card doesn't.
			if !slices.Contains(numaNodeSetFallback, int64(cardNUMA)) && slices.Contains(numaNodeSetFallback, int64(gpu.NUMANode)) {
				pciAddress = gpu.PCIAddress
				vfID = vfs[0]
				cardAvailable = len(vfs)
				cardTotal = int(gpu.SRIOV.CurrentVFs)
				cardNUMA = int(gpu.NUMANode)

				continue
			}

			// Skip current card if we already have a card matching a fallback NUMA node and this card isn't on the main or fallback node.
			if slices.Contains(numaNodeSetFallback, int64(cardNUMA)) && !slices.Contains(numaNodeSetFallback, int64(gpu.NUMANode)) && !slices.Contains(numaNodeSet, int64(gpu.NUMANode)) {
				continue
			}
		}

		// Prioritize less busy cards.
		if pciAddress == "" || (float64(len(vfs))/float64(gpu.SRIOV.CurrentVFs)) > (float64(cardAvailable)/float64(cardTotal)) {
			pciAddress = gpu.PCIAddress
			vfID = vfs[0]
			cardAvailable = len(vfs)
			cardTotal = int(gpu.SRIOV.CurrentVFs)
			cardNUMA = int(gpu.NUMANode)

			continue
		}
	}

	// Check if any physical GPU was found to match.
	if pciAddress == "" {
		return "", -1, fmt.Errorf("Couldn't find a matching GPU with available VFs")
	}

	return pciAddress, vfID, nil
}

// setupSriovParent configures a SR-IOV virtual function (VF) device on parent and stores original properties of
// the physical device into voltatile for restoration on detach. Returns VF PCI device info.
func (d *gpuSRIOV) setupSriovParent(parentPCIAddress string, vfID int, volatile map[string]string) (pcidev.Device, error) {
	revert := revert.New()
	defer revert.Fail()

	volatile["last_state.pci.parent"] = parentPCIAddress
	volatile["last_state.vf.id"] = fmt.Sprintf("%d", vfID)
	volatile["last_state.created"] = "false" // Indicates don't delete device at stop time.

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCIDev, err := d.getVFDevicePCISlot(parentPCIAddress, volatile["last_state.vf.id"])
	if err != nil {
		return vfPCIDev, err
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = pcidev.DeviceUnbind(vfPCIDev)
	if err != nil {
		return vfPCIDev, err
	}

	revert.Add(func() { _ = pcidev.DeviceProbe(vfPCIDev) })

	// Register VF device with vfio-pci driver so it can be passed to VM.
	err = pcidev.DeviceDriverOverride(vfPCIDev, "vfio-pci")
	if err != nil {
		return vfPCIDev, err
	}

	// Record original driver used by VF device for restore.
	volatile["last_state.pci.driver"] = vfPCIDev.Driver

	revert.Success()

	return vfPCIDev, nil
}

// getVFDevicePCISlot returns the PCI slot name for a PCI virtual function device.
func (d *gpuSRIOV) getVFDevicePCISlot(parentPCIAddress string, vfID string) (pcidev.Device, error) {
	ueventFile := fmt.Sprintf("/sys/bus/pci/devices/%s/virtfn%s/uevent", parentPCIAddress, vfID)
	pciDev, err := pcidev.ParseUeventFile(ueventFile)
	if err != nil {
		return pciDev, err
	}

	return pciDev, nil
}

// Stop is run when the device is removed from the instance.
func (d *gpuSRIOV) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuSRIOV) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"last_state.created":    "",
			"last_state.vf.id":      "",
			"last_state.pci.driver": "",
			"last_state.pci.parent": "",
		})
	}()

	v := d.volatileGet()

	err := d.restoreSriovParent(v)
	if err != nil {
		return err
	}

	return nil
}

// restoreSriovParent restores SR-IOV parent device settings when removed from an instance using the
// volatile data that was stored when the device was first added with setupSriovParent().
func (d *gpuSRIOV) restoreSriovParent(volatile map[string]string) error {
	// Nothing to do if we don't know the original device name or the VF ID.
	if volatile["last_state.pci.parent"] == "" || volatile["last_state.vf.id"] == "" || (d.config["pci"] == "" && d.config["id"] == "" && d.config["vendorid"] == "" && d.config["productid"] == "") {
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Get VF device's PCI info so we can unbind and rebind it from the host.
	vfPCIDev, err := d.getVFDevicePCISlot(volatile["last_state.pci.parent"], volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the restored settings will take effect when we rebind it.
	err = pcidev.DeviceUnbind(vfPCIDev)
	if err != nil {
		return err
	}

	if d.inst.Type() == instancetype.VM {
		// Before we bind the device back to the host, ensure we restore the original driver info as it
		// should be currently set to vfio-pci.
		err = pcidev.DeviceSetDriverOverride(vfPCIDev, volatile["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	revert.Add(func() { _ = pcidev.DeviceProbe(vfPCIDev) })

	// Bind VF device onto the host so that the settings will take effect.
	err = pcidev.DeviceProbe(vfPCIDev)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
