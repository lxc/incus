package device

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/lxc/incus/v6/internal/revert"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/network"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

type nicMACVLAN struct {
	deviceCommon

	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicMACVLAN) CanHotPlug() bool {
	return true
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicMACVLAN) CanMigrate() bool {
	return d.config["network"] != ""
}

// validateConfig checks the supplied config for correctness.
func (d *nicMACVLAN) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	var requiredFields []string
	optionalFields := []string{
		"name",
		"network",
		"parent",
		"mtu",
		"hwaddr",
		"vlan",
		"boot.priority",
		"gvrp",
	}

	// Check that if network proeperty is set that conflicting keys are not present.
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "gvrp"}
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

		if d.network.Type() != "macvlan" {
			return fmt.Errorf("Specified network must be of type macvlan")
		}

		netConfig := d.network.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// Copy certain keys verbatim from the network's settings.
		inheritKeys := []string{"mtu", "vlan", "gvrp"}
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
func (d *nicMACVLAN) PreStartCheck() error {
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
func (d *nicMACVLAN) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicMACVLAN) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	actualParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

	// Record the temporary device name used for deletion later.
	saveData["host_name"], err = d.generateHostName("mac", d.config["hwaddr"])
	if err != nil {
		return nil, err
	}

	// Create VLAN parent device if needed.
	statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], actualParentName, d.config["vlan"], util.IsTrue(d.config["gvrp"]))
	if err != nil {
		return nil, err
	}

	// Record whether we created the parent device or not so it can be removed on stop.
	saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

	if util.IsTrue(saveData["last_state.created"]) {
		revert.Add(func() {
			_ = networkRemoveInterfaceIfNeeded(d.state, actualParentName, d.inst, d.config["parent"], d.config["vlan"])
		})
	}

	// Create MACVLAN interface.
	link := &ip.Macvlan{
		Link: ip.Link{
			Name:   saveData["host_name"],
			Parent: actualParentName,
		},
		Mode: "bridge",
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		hwaddr, err := net.ParseMAC(d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed parsing MAC address %q: %w", d.config["hwaddr"], err)
		}

		link.Address = hwaddr
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		mtu, err := strconv.ParseUint(d.config["mtu"], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Invalid MTU specified %q: %w", d.config["mtu"], err)
		}

		link.MTU = uint32(mtu)
	}

	if d.inst.Type() == instancetype.VM {
		// Enable all multicast processing which is required for IPv6 NDP functionality.
		link.AllMutlicast = true

		// Bring the interface up on host side.
		link.Up = true

		// Create macvtap interface using common macvlan settings.
		link := &ip.Macvtap{
			Macvlan: *link,
		}

		err = link.Add()
		if err != nil {
			return nil, err
		}
	} else {
		// Create macvlan interface.
		err = link.Add()
		if err != nil {
			return nil, err
		}
	}

	revert.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

	if d.inst.Type() == instancetype.VM {
		// Disable IPv6 on host interface to avoid getting IPv6 link-local addresses unnecessarily.
		err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", link.Name), "1")
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to disable IPv6 on host interface %q: %w", link.Name, err)
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
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "mtu", Value: d.config["mtu"]},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicMACVLAN) Stop() (*deviceConfig.RunConfig, error) {
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
func (d *nicMACVLAN) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":          "",
			"last_state.hwaddr":  "",
			"last_state.mtu":     "",
			"last_state.created": "",
		})
	}()

	errs := []error{}
	v := d.volatileGet()

	// Delete the detached device.
	if v["host_name"] != "" && util.PathExists(fmt.Sprintf("/sys/class/net/%s", v["host_name"])) {
		err := network.InterfaceRemove(v["host_name"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	// This will delete the parent interface if we created it for VLAN parent.
	if util.IsTrue(v["last_state.created"]) {
		actualParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])
		err := networkRemoveInterfaceIfNeeded(d.state, actualParentName, d.inst, d.config["parent"], d.config["vlan"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}
