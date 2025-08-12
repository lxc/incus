package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/db"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	pcidev "github.com/lxc/incus/v6/internal/server/device/pci"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/resources"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/util"
)

type nicPhysical struct {
	deviceCommon

	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicPhysical) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}
	optionalFields := []string{
		// gendoc:generate(entity=devices, group=nic_physical, key=parent)
		//
		// ---
		//  type: string
		//  managed: yes
		//  shortdesc: The name of the parent host device (required if specifying the `nictype` directly)
		"parent",

		// gendoc:generate(entity=devices, group=nic_physical, key=name)
		//
		// ---
		//  type: string
		//  default: kernel assigned
		//  managed: no
		//  shortdesc: The name of the interface inside the instance
		"name",

		// gendoc:generate(entity=devices, group=nic_physical, key=boot.priority)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: Boot priority for VMs (higher value boots first)
		"boot.priority",

		// gendoc:generate(entity=devices, group=nic_physical, key=gvrp)
		//
		// ---
		//  type: bool
		//  default: false
		//  managed: no
		//  shortdesc: Register VLAN using GARP VLAN Registration Protocol
		"gvrp",

		// gendoc:generate(entity=devices, group=nic_physical, key=mtu)
		//
		// ---
		//  type: integer
		//  default: MTU of the parent device
		//  managed: no
		//  shortdesc: The Maximum Transmit Unit (MTU) of the new interface
		"mtu",
	}

	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		// gendoc:generate(entity=devices, group=nic_physical, key=vlan)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: The VLAN ID to attach to
		optionalFields = append(optionalFields, "hwaddr", "vlan")
	}

	// gendoc:generate(entity=devices, group=nic_physical, key=network)
	//
	// ---
	//  type: string
	//  managed: no
	//  shortdesc: The managed network to link the device to (instead of specifying the `nictype` directly)
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "gvrp"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		// api.ProjectDefaultName is used here as physical networks don't support projects.
		var err error
		d.network, err = network.LoadByName(d.state, api.ProjectDefaultName, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		if d.network.Status() != api.NetworkStatusCreated {
			return errors.New("Specified network is not fully created")
		}

		if d.network.Type() != "physical" {
			return errors.New("Specified network must be of type physical")
		}

		netConfig := d.network.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// If parent is a bridge, ensure it's managed.
		isParentBridge := d.config["parent"] != "" && util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"]))
		if isParentBridge && d.network == nil {
			return fmt.Errorf("Parent device is a bridge, use nictype=bridged instead")
		}

		// gendoc:generate(entity=devices, group=nic_physical, key=hwaddr)
		//
		// ---
		//  type: string
		//  default: randomly assigned
		//  managed: no
		//  shortdesc: The MAC address of the new interface
		optionalFields = append(optionalFields, "hwaddr")

		// Copy certain keys verbatim from the network's settings.
		for _, field := range optionalFields {
			_, found := netConfig[field]
			if found {
				d.config[field] = netConfig[field]
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

// validateEnvironment checks the runtime environment for correctness.
func (d *nicPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && util.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return errors.New("Network physical devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return errors.New("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Handle the case where the parent is a bridge.
	isParentBridge := util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"]))
	if isParentBridge {
		// Convert the device to a nictype=bridged internally.
		bridgedConfig := d.config.Clone()
		bridgedConfig["type"] = "nic"
		bridgedConfig["nictype"] = "bridged"
		bridgedConfig["network"] = ""

		// Instantiate the new device.
		bridged, err := load(d.inst, d.state, d.inst.Project().Name, d.inst.Name(), bridgedConfig, d.volatileGet, d.volatileSet)
		if err != nil {
			return nil, fmt.Errorf("Failed to initialize bridged device: %w", err)
		}

		// Forward the start call.
		return bridged.Start()
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	reverter := revert.New()
	defer reverter.Fail()

	// pciIOMMUGroup, used for VM physical passthrough.
	var pciIOMMUGroup uint64

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = linux.LoadModule("vfio-pci")
		if err != nil {
			return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
		}
	}

	// Record the host_name device used for restoration later.
	saveData["host_name"] = network.GetHostDevice(d.config["parent"], d.config["vlan"])

	if d.inst.Type() == instancetype.Container {
		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], saveData["host_name"], d.config["vlan"], util.IsTrue(d.config["gvrp"]))
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		if util.IsTrue(saveData["last_state.created"]) {
			reverter.Add(func() {
				_ = networkRemoveInterfaceIfNeeded(d.state, saveData["host_name"], d.inst, d.config["parent"], d.config["vlan"])
			})
		}

		// If we didn't create the device we should track various properties so we can restore them when the
		// instance is stopped or the device is detached.
		if util.IsFalse(saveData["last_state.created"]) {
			err = networkSnapshotPhysicalNIC(saveData["host_name"], saveData)
			if err != nil {
				return nil, err
			}
		}

		// Set the MAC address.
		if d.config["hwaddr"] != "" {
			hwaddr, err := net.ParseMAC(d.config["hwaddr"])
			if err != nil {
				return nil, fmt.Errorf("Failed parsing MAC address %q: %w", d.config["hwaddr"], err)
			}

			link := &ip.Link{Name: saveData["host_name"]}
			err = link.SetAddress(hwaddr)
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
	} else if d.inst.Type() == instancetype.VM {
		// Try to get PCI information about the network interface.
		ueventPath := fmt.Sprintf("/sys/class/net/%s/device/uevent", saveData["host_name"])
		pciDev, err := pcidev.ParseUeventFile(ueventPath)
		if err != nil {
			if errors.Is(err, pcidev.ErrDeviceIsUSB) {
				// Device is USB rather than PCI.
				return d.startVMUSB(saveData["host_name"])
			}

			return nil, fmt.Errorf("Failed to get PCI device info for %q: %w", saveData["host_name"], err)
		}

		saveData["last_state.pci.slot.name"] = pciDev.SlotName
		saveData["last_state.pci.driver"] = pciDev.Driver

		pciIOMMUGroup, err = pcidev.DeviceIOMMUGroup(saveData["last_state.pci.slot.name"])
		if err != nil {
			return nil, err
		}

		err = pcidev.DeviceDriverOverride(pciDev, "vfio-pci")
		if err != nil {
			return nil, err
		}
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

	reverter.Success()

	return &runConf, nil
}

func (d *nicPhysical) startVMUSB(name string) (*deviceConfig.RunConfig, error) {
	// Get the list of network interfaces.
	interfaces, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	// Look for our USB device.
	var addr string
	for _, card := range interfaces.Cards {
		for _, port := range card.Ports {
			if port.ID == name {
				addr = card.USBAddress
				break
			}
		}

		if addr != "" {
			break
		}
	}

	if addr == "" {
		return nil, fmt.Errorf("Failed to get USB device info for %q", name)
	}

	// Parse the USB address.
	fields := strings.Split(addr, ":")
	if len(fields) != 2 {
		return nil, fmt.Errorf("Bad USB device info for %q", name)
	}

	usbBus, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("Bad USB device info for %q: %w", name, err)
	}

	usbDev, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("Bad USB device info for %q: %w", name, err)
	}

	// Record the addresses.
	saveData := map[string]string{}
	saveData["last_state.usb.bus"] = fmt.Sprintf("%03d", usbBus)
	saveData["last_state.usb.device"] = fmt.Sprintf("%03d", usbDev)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	// Generate a config.
	runConf := deviceConfig.RunConfig{}
	runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
		DeviceName:     fmt.Sprintf("%s-%03d-%03d", d.name, usbBus, usbDev),
		HostDevicePath: fmt.Sprintf("/dev/bus/usb/%03d/%03d", usbBus, usbDev),
	})

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicPhysical) Stop() (*deviceConfig.RunConfig, error) {
	// Handle the case where the parent is a bridge.
	isParentBridge := util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", d.config["parent"]))
	if isParentBridge {
		// Convert the device to a nictype=bridged internally.
		bridgedConfig := d.config.Clone()
		bridgedConfig["type"] = "nic"
		bridgedConfig["nictype"] = "bridged"
		bridgedConfig["network"] = ""

		// Instantiate the new device.
		bridged, err := load(d.inst, d.state, d.inst.Project().Name, d.inst.Name(), bridgedConfig, d.volatileGet, d.volatileSet)
		if err != nil {
			return nil, fmt.Errorf("Failed to initialize bridged device: %w", err)
		}

		// Forward the stop call.
		return bridged.Stop()
	}

	v := d.volatileGet()

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if v["last_state.usb.bus"] != "" && v["last_state.usb.device"] != "" {
		// Handle USB NICs.
		runConf.USBDevice = append(runConf.USBDevice, deviceConfig.USBDeviceItem{
			DeviceName:     fmt.Sprintf("%s-%s-%s", d.name, v["last_state.usb.bus"], v["last_state.usb.device"]),
			HostDevicePath: fmt.Sprintf("/dev/bus/usb/%s/%s", v["last_state.usb.bus"], v["last_state.usb.device"]),
		})
	} else {
		// Handle all other NICs.
		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.created":       "",
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"last_state.usb.bus":       "",
			"last_state.usb.device":    "",
		})
	}()

	v := d.volatileGet()

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		vfioDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := pcidev.DeviceDriverOverride(vfioDev, v["last_state.pci.driver"])
		if err != nil {
			return err
		}
	} else if d.inst.Type() == instancetype.Container {
		hostName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

		// This will delete the parent interface if we created it for VLAN parent.
		if util.IsTrue(v["last_state.created"]) {
			err := networkRemoveInterfaceIfNeeded(d.state, hostName, d.inst, d.config["parent"], d.config["vlan"])
			if err != nil {
				return err
			}
		} else if v["last_state.pci.slot.name"] == "" {
			err := networkRestorePhysicalNIC(hostName, v)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// IsPhysicalNICWithBridge returns true if the given NIC is of type "physical"
// and has a non-empty Parent field, indicating it's attached to a bridge.
func IsPhysicalNICWithBridge(s *state.State, deviceProjectName string, d deviceConfig.Device) bool {
	if d["network"] != "" {
		// Translate device's project name into a network project name.
		networkProjectName, _, err := project.NetworkProject(s.DB.Cluster, deviceProjectName)
		if err != nil {
			return false
		}

		var netInfo *api.Network

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, netInfo, _, err = tx.GetNetworkInAnyState(ctx, networkProjectName, d["network"])

			return err
		})
		if err != nil {
			return false
		}

		if netInfo.Type != "physical" {
			return false
		}

		parent := netInfo.Config["parent"]
		if parent == "" {
			return false
		}

		return util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", parent))
	}

	return false
}
