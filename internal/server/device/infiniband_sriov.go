package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/revert"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

type infinibandSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *infinibandSRIOV) validateConfig(instConf instance.ConfigReader) error {
	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)
	rules["hwaddr"] = func(value string) error {
		if value == "" {
			return nil
		}

		return infinibandValidMAC(value)
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *infinibandSRIOV) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

func (d *infinibandSRIOV) startContainer() (*deviceConfig.RunConfig, error) {
	saveData := make(map[string]string)

	// Load network interface info.
	nics, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	// Filter the network interfaces to just infiniband devices related to parent.
	ibDevs := infinibandDevices(nics, d.config["parent"])

	// We don't count the parent as an available VF.
	delete(ibDevs, d.config["parent"])

	// Load any interfaces already allocated to other devices.
	reservedDevices, err := network.SRIOVGetHostDevicesInUse(d.state)
	if err != nil {
		return nil, err
	}

	// Remove reserved devices from available list.
	for k := range reservedDevices {
		delete(ibDevs, k)
	}

	if len(ibDevs) < 1 {
		return nil, fmt.Errorf("All virtual functions on parent device are already in use")
	}

	// Get first VF device that is free.
	var vfDev *api.ResourcesNetworkCardPort
	for _, v := range ibDevs {
		vfDev = v
		break
	}

	saveData["host_name"] = vfDev.ID

	// Record hwaddr and mtu before potentially modifying them.
	err = networkSnapshotPhysicalNIC(saveData["host_name"], saveData)
	if err != nil {
		return nil, err
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		err := infinibandSetDevMAC(saveData["host_name"], d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		mtu, err := strconv.ParseUint(d.config["mtu"], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Invalid MTU specified %q: %w", d.config["mtu"], err)
		}

		link := &ip.Link{Name: saveData["host_name"]}
		err = link.SetMTU(uint32(mtu))
		if err != nil {
			return nil, fmt.Errorf("Failed setting MTU %q on %q: %w", d.config["mtu"], saveData["host_name"], err)
		}
	}

	runConf := deviceConfig.RunConfig{}

	// Configure runConf with infiniband setup instructions.
	err = infinibandAddDevices(d.state, d.inst.DevicesPath(), d.name, vfDev, &runConf)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	return &runConf, nil
}

func (d *infinibandSRIOV) startVM() (*deviceConfig.RunConfig, error) {
	saveData := make(map[string]string)

	err := linux.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Load network interface info.
	nics, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	var parentPCIAddress string

	for _, card := range nics.Cards {
		found := false

		for _, port := range card.Ports {
			if port.ID == d.config["parent"] {
				found = true
				break
			}
		}

		if !found {
			continue
		}

		parentPCIAddress = card.PCIAddress
		break
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", parentPCIAddress)

	pciParentDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, fmt.Errorf("Failed to get PCI device info for %q: %w", parentPCIAddress, err)
	}

	vfID, err := d.findFreeVirtualFunction(pciParentDev)
	if err != nil {
		return nil, fmt.Errorf("Failed to find free virtual function: %w", err)
	}

	if vfID == -1 {
		return nil, fmt.Errorf("All virtual functions on parent device are already in use")
	}

	vfPCIDev, err := d.setupSriovParent(parentPCIAddress, vfID, saveData)
	if err != nil {
		return nil, err
	}

	pciIOMMUGroup, err := pcidev.DeviceIOMMUGroup(vfPCIDev.SlotName)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}

	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
	}

	runConf.NetworkInterface = append(runConf.NetworkInterface, []deviceConfig.RunConfigItem{
		{Key: "devName", Value: d.name},
		{Key: "pciSlotName", Value: vfPCIDev.SlotName},
		{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
	}...)

	return &runConf, nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *infinibandSRIOV) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

// Stop is run when the device is removed from the instance.
func (d *infinibandSRIOV) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks:        []func() error{d.postStop},
		NetworkInterface: []deviceConfig.RunConfigItem{{Key: "link", Value: v["host_name"]}},
	}

	if d.inst.Type() == instancetype.Container {
		err := unixDeviceRemove(d.inst.DevicesPath(), IBDevPrefix, d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *infinibandSRIOV) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"last_state.pci.parent":    "",
		})
	}()

	if d.inst.Type() == instancetype.Container {
		// Remove infiniband host files for this device.
		err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), IBDevPrefix, d.name, "")
		if err != nil {
			return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
		}
	}

	// Restore hwaddr and mtu.
	v := d.volatileGet()
	if v["host_name"] != "" {
		err := networkRestorePhysicalNIC(v["host_name"], v)
		if err != nil {
			return err
		}
	}

	// Unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		pciDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		// Unbind VF device from the host so that the restored settings will take effect when we rebind it.
		err := pcidev.DeviceUnbind(pciDev)
		if err != nil {
			return err
		}

		err = pcidev.DeviceDriverOverride(pciDev, v["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	return nil
}

// setupSriovParent configures a SR-IOV virtual function (VF) device on parent and stores original properties of
// the physical device into voltatile for restoration on detach. Returns VF PCI device info.
func (d *infinibandSRIOV) setupSriovParent(parentPCIAddress string, vfID int, volatile map[string]string) (pcidev.Device, error) {
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
func (d *infinibandSRIOV) getVFDevicePCISlot(parentPCIAddress string, vfID string) (pcidev.Device, error) {
	ueventFile := fmt.Sprintf("/sys/bus/pci/devices/%s/virtfn%s/uevent", parentPCIAddress, vfID)
	pciDev, err := pcidev.ParseUeventFile(ueventFile)
	if err != nil {
		return pciDev, err
	}

	return pciDev, nil
}

func (d *infinibandSRIOV) findFreeVirtualFunction(parentDev pcidev.Device) (int, error) {
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
