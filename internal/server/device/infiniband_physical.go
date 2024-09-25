package device

import (
	"fmt"
	"strconv"

	"github.com/lxc/incus/v6/internal/linux"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/util"
)

type infinibandPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *infinibandPhysical) validateConfig(instConf instance.ConfigReader) error {
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
func (d *infinibandPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *infinibandPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	// pciIOMMUGroup, used for VM physical passthrough.
	var pciIOMMUGroup uint64

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = linux.LoadModule("vfio-pci")
		if err != nil {
			return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
		}
	}

	runConf := deviceConfig.RunConfig{}

	// Load network interface info.
	nics, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	// Filter the network interfaces to just infiniband devices related to parent.
	ibDevs := infinibandDevices(nics, d.config["parent"])
	ibDev, found := ibDevs[d.config["parent"]]
	if !found {
		return nil, fmt.Errorf("Specified infiniband device \"%s\" not found", d.config["parent"])
	}

	saveData["host_name"] = ibDev.ID

	if d.inst.Type() == instancetype.Container {
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

		// Configure runConf with infiniband setup instructions.
		err = infinibandAddDevices(d.state, d.inst.DevicesPath(), d.name, ibDev, &runConf)
		if err != nil {
			return nil, err
		}
	} else if d.inst.Type() == instancetype.VM {
		// Get PCI information about the network interface.
		ueventPath := fmt.Sprintf("/sys/class/net/%s/device/uevent", saveData["host_name"])
		pciDev, err := pcidev.ParseUeventFile(ueventPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to get PCI device info for %q: %w", saveData["host_name"], err)
		}

		saveData["last_state.pci.slot.name"] = pciDev.SlotName
		saveData["last_state.pci.driver"] = pciDev.Driver

		err = pcidev.DeviceDriverOverride(pciDev, "vfio-pci")
		if err != nil {
			return nil, err
		}

		pciIOMMUGroup, err = pcidev.DeviceIOMMUGroup(saveData["last_state.pci.slot.name"])
		if err != nil {
			return nil, err
		}

		// Record original driver used by device for restore.
		saveData["last_state.pci.driver"] = pciDev.Driver
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

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
				{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
			}...)
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *infinibandPhysical) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
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
func (d *infinibandPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
		})
	}()

	v := d.volatileGet()

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		vfioDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		// Unbind device from the host so that the restored settings will take effect when we rebind it.
		err := pcidev.DeviceUnbind(vfioDev)
		if err != nil {
			return err
		}

		err = pcidev.DeviceDriverOverride(vfioDev, v["last_state.pci.driver"])
		if err != nil {
			return err
		}
	} else if d.inst.Type() == instancetype.Container {
		// Remove infiniband host files for this device.
		err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), IBDevPrefix, d.name, "")
		if err != nil {
			return fmt.Errorf("Failed to delete files for device '%s': %w", d.name, err)
		}
	}

	// Restore hwaddr and mtu.
	if v["host_name"] != "" {
		err := networkRestorePhysicalNIC(v["host_name"], v)
		if err != nil {
			return err
		}
	}

	return nil
}
