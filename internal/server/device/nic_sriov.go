package device

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/lxc/incus/v6/internal/linux"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/util"
)

type nicSRIOV struct {
	deviceCommon

	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicSRIOV) CanHotPlug() bool {
	return true
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicSRIOV) CanMigrate() bool {
	return d.config["network"] != ""
}

// validateConfig checks the supplied config for correctness.
func (d *nicSRIOV) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	var requiredFields []string
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_sriov, key=name)
		//
		// ---
		//  type: string
		//  default: kernel assigned
		//  managed: no
		//  shortdesc: The name of the interface inside the instance
		"name",

		// gendoc:generate(entity=devices, group=nic_sriov, key=network)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: The managed network to link the device to (instead of specifying the `nictype` directly)
		"network",

		// gendoc:generate(entity=devices, group=nic_sriov, key=parent)
		//
		// ---
		//  type: string
		//  managed: yes
		//  shortdesc: The name of the parent host device (required if specifying the `nictype` directly)
		"parent",

		// gendoc:generate(entity=devices, group=nic_sriov, key=hwaddr)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  managed: no
		//  shortdesc: The MAC address of the new interface
		"hwaddr",

		// gendoc:generate(entity=devices, group=nic_sriov, key=mtu)
		//
		// ---
		//  type: integer
		//  default: kernel assigned
		//  managed: yes
		//  shortdesc: The Maximum Transmit Unit (MTU) of the new interface
		"mtu",

		// gendoc:generate(entity=devices, group=nic_sriov, key=vlan)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: The VLAN ID to attach to
		"vlan",

		// gendoc:generate(entity=devices, group=nic_sriov, key=security.mac_filtering)
		//
		// ---
		//  type: bool
		//  default: false
		//  managed: no
		//  shortdesc: Prevent the instance from spoofing another instance's MAC address
		"security.mac_filtering",

		// gendoc:generate(entity=devices, group=nic_sriov, key=boot.priority)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: Boot priority for VMs (higher value boots first)
		"boot.priority",

		// gendoc:generate(entity=devices, group=nic_sriov, key=vendorid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The vendor ID of the parent host device
		"vendorid",

		// gendoc:generate(entity=devices, group=nic_sriov, key=productid)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The product ID of the parent host device
		"productid",

		// gendoc:generate(entity=devices, group=nic_sriov, key=pci)
		//
		// ---
		//  type: string
		//  required: no
		//  shortdesc: The PCI address of the parent host device
		"pci",

		// gendoc:generate(entity=devices, group=nic_sriov, key=attached)
		//
		// ---
		//  type: bool
		//  default: `true`
		//  required: no
		//  shortdesc: Whether the NIC is plugged in or not
		"attached",
	}

	// Check that if network property is set that conflicting keys are not present.
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		// api.ProjectDefaultName is used here as macvlan networks don't support projects.
		var err error
		d.network, err = network.LoadByName(d.state, api.ProjectDefaultName, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		if d.network.Status() != api.NetworkStatusCreated {
			return errors.New("Specified network is not fully created")
		}

		if d.network.Type() != "sriov" {
			return errors.New("Specified network must be of type macvlan")
		}

		netConfig := d.network.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// Copy certain keys verbatim from the network's settings.
		inheritKeys := []string{"mtu", "vlan"}
		for _, inheritKey := range inheritKeys {
			_, found := netConfig[inheritKey]
			if found {
				d.config[inheritKey] = netConfig[inheritKey]
			}
		}
	} else if d.isParentRequired() {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields, instConf))
	if err != nil {
		return err
	}

	if d.config["parent"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "parent" is set`, field)
			}
		}
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"parent", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "pci" is set`, field)
			}
		}

		d.config["pci"] = pcidev.NormaliseAddress(d.config["pci"])
	}

	return nil
}

// PreStartCheck checks the managed parent network is available (if relevant).
func (d *nicSRIOV) PreStartCheck() error {
	// Non-managed network NICs are not relevant for checking managed network availability.
	if d.network == nil {
		return nil
	}

	// If managed network is not available, don't try and start instance.
	if d.network.LocalStatus() == api.NetworkStatusUnavailable {
		return api.StatusErrorf(http.StatusServiceUnavailable, "Network %q unavailable on this server", d.network.Name())
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicSRIOV) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return errors.New("Network SR-IOV devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return errors.New("Requires name property to start")
	}

	if d.isParentRequired() && !network.InterfaceExists(d.config["parent"]) {
		return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicSRIOV) Start() (*deviceConfig.RunConfig, error) {
	// Ignore detached NICs.
	if !util.IsTrueOrEmpty(d.config["attached"]) {
		return nil, nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = linux.LoadModule("vfio-pci")
		if err != nil {
			return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
		}
	}

	parent := d.config["parent"]

	// Try to find parent if not set.
	if parent == "" {
		parent, err = d.findParent()
		if err != nil {
			return nil, err
		}
	}

	// Find free VF exclusively.
	network.SRIOVVirtualFunctionMutex.Lock()
	vfDev, vfID, err := network.SRIOVFindFreeVirtualFunction(d.state, parent)
	if err != nil {
		network.SRIOVVirtualFunctionMutex.Unlock()
		return nil, err
	}

	// Claim the SR-IOV virtual function (VF) on the parent (PF) and get the PCI information.
	vfPCIDev, pciIOMMUGroup, err := networkSRIOVSetupVF(d.deviceCommon, parent, vfDev, vfID, saveData)
	if err != nil {
		network.SRIOVVirtualFunctionMutex.Unlock()
		return nil, err
	}

	network.SRIOVVirtualFunctionMutex.Unlock()

	if d.inst.Type() == instancetype.Container {
		err := networkSRIOVSetupContainerVFNIC(saveData["host_name"], d.inst.MACPattern(), d.config)
		if err != nil {
			return nil, err
		}
	}

	// Save new volatile keys.
	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Get all volatile keys.
	volatile := d.volatileGet()

	// Apply stable MAC address.
	if d.config["hwaddr"] == "" {
		d.config["hwaddr"] = volatile["hwaddr"]
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: vfPCIDev.SlotName},
				{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
			}...)
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicSRIOV) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if util.IsTrueOrEmpty(d.config["attached"]) {
		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicSRIOV) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.created":       "",
			"last_state.vf.parent":     "",
			"last_state.vf.id":         "",
			"last_state.vf.hwaddr":     "",
			"last_state.vf.vlan":       "",
			"last_state.vf.spoofcheck": "",
			"last_state.pci.driver":    "",
		})
	}()

	v := d.volatileGet()

	network.SRIOVVirtualFunctionMutex.Lock()
	err := networkSRIOVRestoreVF(d.deviceCommon, true, v)
	if err != nil {
		network.SRIOVVirtualFunctionMutex.Unlock()
		return err
	}

	network.SRIOVVirtualFunctionMutex.Unlock()

	return nil
}

// findParent selects the best NIC based on vendorid, productid or PCI address,
// considering NUMA nodes.
func (d *nicSRIOV) findParent() (string, error) {
	// List all the NICs.
	interfaces, err := resources.GetNetwork()
	if err != nil {
		return "", err
	}

	numaNodeSet, numaNodeSetFallback, err := getNumaNodeSet(d.inst.ExpandedConfig())
	if err != nil {
		return "", err
	}

	parent := ""
	vfFreeRatio := 0.0
	cardNUMA := -1

	for _, nic := range interfaces.Cards {
		// Skip any cards that are not selected.
		if !nicSelected(d.Config(), nic) {
			continue
		}

		// Skip any card without SR-IOV.
		if nic.SRIOV == nil {
			d.logger.Debug("Skip card without SR-IOV", logger.Ctx{"pci": nic.PCIAddress})
			continue
		}

		// Find available VFs.
		currentVfFreeRatio := 0.0
		currentParent := ""

		network.SRIOVVirtualFunctionMutex.Lock()
		for _, port := range nic.Ports {
			freeVf, totalVf, err := network.SRIOVCountFreeVirtualFunctions(d.state, port.ID)
			if err != nil {
				network.SRIOVVirtualFunctionMutex.Unlock()
				return "", err
			}

			tmpRatio := float64(freeVf) / float64(totalVf)
			if tmpRatio > currentVfFreeRatio {
				currentVfFreeRatio = tmpRatio
				currentParent = port.ID
			}
		}

		network.SRIOVVirtualFunctionMutex.Unlock()

		// Skip if no available VFs.
		if currentVfFreeRatio == 0 {
			d.logger.Debug("No available VFs on card", logger.Ctx{"pci": nic.PCIAddress})
			continue
		}

		// Handle NUMA.
		if numaNodeSet != nil {
			// Switch to current card if it matches our main NUMA node and existing card doesn't.
			if !slices.Contains(numaNodeSet, int64(cardNUMA)) && slices.Contains(numaNodeSet, int64(nic.NUMANode)) {
				parent = currentParent
				vfFreeRatio = currentVfFreeRatio
				cardNUMA = int(nic.NUMANode)

				continue
			}

			// Skip current card if we already have a card matching our main NUMA node and this card doesn't.
			if slices.Contains(numaNodeSet, int64(cardNUMA)) && !slices.Contains(numaNodeSet, int64(nic.NUMANode)) {
				continue
			}

			// Switch to current card if it matches a fallback NUMA node and existing card doesn't.
			if !slices.Contains(numaNodeSetFallback, int64(cardNUMA)) && slices.Contains(numaNodeSetFallback, int64(nic.NUMANode)) {
				parent = currentParent
				vfFreeRatio = currentVfFreeRatio
				cardNUMA = int(nic.NUMANode)

				continue
			}

			// Skip current card if we already have a card matching a fallback NUMA node and this card isn't on the main or fallback node.
			if slices.Contains(numaNodeSetFallback, int64(cardNUMA)) && !slices.Contains(numaNodeSetFallback, int64(nic.NUMANode)) && !slices.Contains(numaNodeSet, int64(nic.NUMANode)) {
				continue
			}
		}

		// Prioritize less busy cards.
		if parent == "" || currentVfFreeRatio > vfFreeRatio {
			parent = currentParent
			vfFreeRatio = currentVfFreeRatio
			cardNUMA = int(nic.NUMANode)

			d.logger.Debug("Selected NIC", logger.Ctx{"PCI": nic.PCIAddress, "parent": parent})

			continue
		}
	}

	// Check if any NIC was found to match.
	if parent == "" {
		return "", errors.New("Couldn't find a matching NIC")
	}

	return parent, nil
}

// isParentRequired checks whether the parent config option is required.
func (d *nicSRIOV) isParentRequired() bool {
	if d.config["pci"] == "" && d.config["vendorid"] == "" && d.config["productid"] == "" {
		return true
	}

	return false
}

// Check if the device matches the given NIC.
// It matches based on vendorid, productid or pci setting of the device.
func nicSelected(device deviceConfig.Device, nic api.ResourcesNetworkCard) bool {
	if device["pci"] != "" && nic.PCIAddress == device["pci"] {
		return true
	}

	if device["vendorid"] != "" && device["productid"] != "" {
		if nic.VendorID == device["vendorid"] && nic.ProductID == device["productid"] {
			return true
		}
	} else if device["vendorid"] != "" {
		if nic.VendorID == device["vendorid"] {
			return true
		}
	}

	return false
}
