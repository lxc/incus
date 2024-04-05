package device

import (
	"fmt"
	"os"

	"github.com/lxc/incus/v6/internal/revert"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/network"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/util"
)

type nicP2P struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicP2P) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicP2P) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		"name",
		"mtu",
		"queue.tx.length",
		"hwaddr",
		"host_name",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"limits.priority",
		"ipv4.routes",
		"ipv6.routes",
		"boot.priority",
	}

	err := d.config.Validate(nicValidationRules([]string{}, optionalFields, instConf))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicP2P) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	return nil
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicP2P) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicP2P)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.priority", "ipv4.routes", "ipv6.routes"}
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicP2P) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	var peerName string
	var mtu uint32

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	if d.inst.Type() == instancetype.Container {
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("veth", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}

		peerName, mtu, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("tap", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}

		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		mtu, err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

	// Attempt to disable router advertisement acceptance.
	err = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Apply host-side routes to veth interface.
	err = networkNICRouteAdd(d.config["host_name"], append(util.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true), util.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)...)
	if err != nil {
		return nil, err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, nil, false)
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
		{Key: "link", Value: peerName},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "mtu", Value: fmt.Sprintf("%d", mtu)},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// Update applies configuration changes to a started device.
func (d *nicP2P) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	if !isRunning {
		return nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return err
	}

	oldConfig := oldDevices[d.name]
	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)
	networkVethFillFromVolatile(oldConfig, v)

	// Remove old host-side routes from veth interface.
	networkNICRouteDelete(oldConfig["host_name"], append(util.SplitNTrimSpace(oldConfig["ipv4.routes"], ",", -1, true), util.SplitNTrimSpace(oldConfig["ipv6.routes"], ",", -1, true)...)...)

	// Apply host-side routes to veth interface.
	err = networkNICRouteAdd(d.config["host_name"], append(util.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true), util.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)...)
	if err != nil {
		return err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, oldConfig, false)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicP2P) Stop() (*deviceConfig.RunConfig, error) {
	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	err := networkClearHostVethLimits(&d.deviceCommon)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicP2P) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name": "",
		})
	}()

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %s: %w", d.config["host_name"], err)
		}
	}

	return nil
}
