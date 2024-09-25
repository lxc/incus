package device

import (
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/internal/linux"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/shared/api"
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
		"name",
		"network",
		"parent",
		"hwaddr",
		"mtu",
		"vlan",
		"security.mac_filtering",
		"boot.priority",
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
			return fmt.Errorf("Specified network is not fully created")
		}

		if d.network.Type() != "sriov" {
			return fmt.Errorf("Specified network must be of type macvlan")
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
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields, instConf))
	if err != nil {
		return err
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
		return fmt.Errorf("Network SR-IOV devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !network.InterfaceExists(d.config["parent"]) {
		return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicSRIOV) Start() (*deviceConfig.RunConfig, error) {
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

	// Find free VF exclusively.
	network.SRIOVVirtualFunctionMutex.Lock()
	vfDev, vfID, err := network.SRIOVFindFreeVirtualFunction(d.state, d.config["parent"])
	if err != nil {
		network.SRIOVVirtualFunctionMutex.Unlock()
		return nil, err
	}

	// Claim the SR-IOV virtual function (VF) on the parent (PF) and get the PCI information.
	vfPCIDev, pciIOMMUGroup, err := networkSRIOVSetupVF(d.deviceCommon, d.config["parent"], vfDev, vfID, true, saveData)
	if err != nil {
		network.SRIOVVirtualFunctionMutex.Unlock()
		return nil, err
	}

	network.SRIOVVirtualFunctionMutex.Unlock()

	if d.inst.Type() == instancetype.Container {
		err := networkSRIOVSetupContainerVFNIC(saveData["host_name"], d.config)
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
		NetworkInterface: []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
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
