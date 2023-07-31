package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	deviceConfig "github.com/cyphar/incus/incus/device/config"
	pcidev "github.com/cyphar/incus/incus/device/pci"
	"github.com/cyphar/incus/incus/instance"
	"github.com/cyphar/incus/incus/instance/instancetype"
	"github.com/cyphar/incus/incus/resources"
	"github.com/cyphar/incus/incus/revert"
	"github.com/cyphar/incus/incus/util"
	"github.com/cyphar/incus/shared"
)

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
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
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

	// Get SRIOV parent, i.e. the actual GPU.
	parentPCIAddresses, err := d.getParentPCIAddresses()
	if err != nil {
		return nil, err
	}

	var parentPCIAddress string
	var pciParentDev pcidev.Device
	vfID := -1

	// Make sure that vfio-pci is loaded.
	err = util.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Since there might be multiple GPUs, we iterate through them and get the first free
	// virtual function.
	for _, parentPCIAddress = range parentPCIAddresses {
		// Get PCI information about the GPU device.
		devicePath := filepath.Join("/sys/bus/pci/devices", parentPCIAddress)

		pciParentDev, err = pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
		if err != nil {
			err = fmt.Errorf("Failed to get PCI device info for GPU %q: %w", parentPCIAddress, err)
			continue
		}

		vfID, err = d.findFreeVirtualFunction(pciParentDev)
		if err != nil {
			err = fmt.Errorf("Failed to find free virtual function: %w", err)
			continue
		}

		if vfID > -1 {
			break
		}
	}

	if err != nil {
		return nil, err
	}

	if vfID == -1 {
		return nil, fmt.Errorf("All virtual functions on parent device seem to be in use")
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

// getParentPCIAddresses returns the PCI addresses of parent GPUs.
func (d *gpuSRIOV) getParentPCIAddresses() ([]string, error) {
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	var parentPCIAddresses []string

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		parentPCIAddresses = append(parentPCIAddresses, gpu.PCIAddress)
	}

	if len(parentPCIAddresses) == 0 {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	return parentPCIAddresses, nil
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

func (d *gpuSRIOV) findFreeVirtualFunction(parentDev pcidev.Device) (int, error) {
	// Get number of currently enabled VFs.
	sriovNumVFs := fmt.Sprintf("/sys/bus/pci/devices/%s/sriov_numvfs", parentDev.SlotName)

	sriovNumVfsBuf, err := os.ReadFile(sriovNumVFs)
	if err != nil {
		return 0, err
	}

	sriovNumVfsStr := strings.TrimSpace(string(sriovNumVfsBuf))
	sriovNum, err := strconv.Atoi(sriovNumVfsStr)
	if err != nil {
		return 0, err
	}

	vfID := -1

	for i := 0; i < sriovNum; i++ {
		pciDev, err := pcidev.ParseUeventFile(fmt.Sprintf("/sys/bus/pci/devices/%s/virtfn%d/uevent", parentDev.SlotName, i))
		if err != nil {
			return 0, err
		}

		// We assume the virtual function is free if there's no driver bound to it.
		if pciDev.Driver == "" {
			vfID = i
			break
		}
	}

	return vfID, nil
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
