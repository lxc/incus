package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
)

type cmdConfigDevice struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigDevice) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("device")
	cmd.Short = i18n.G("Manage devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage devices`))

	// Add
	configDeviceAddCmd := cmdConfigDeviceAdd{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceAddCmd.Command())

	// Get
	configDeviceGetCmd := cmdConfigDeviceGet{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceGetCmd.Command())

	// List
	configDeviceListCmd := cmdConfigDeviceList{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceListCmd.Command())

	// Override
	if c.config != nil {
		configDeviceOverrideCmd := cmdConfigDeviceOverride{global: c.global, config: c.config, profile: c.profile, configDevice: c}
		cmd.AddCommand(configDeviceOverrideCmd.Command())
	}

	// Remove
	configDeviceRemoveCmd := cmdConfigDeviceRemove{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceRemoveCmd.Command())

	// Set
	configDeviceSetCmd := cmdConfigDeviceSet{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceSetCmd.Command())

	// Show
	configDeviceShowCmd := cmdConfigDeviceShow{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceShowCmd.Command())

	// Unset
	configDeviceUnsetCmd := cmdConfigDeviceUnset{global: c.global, config: c.config, profile: c.profile, configDevice: c, configDeviceSet: &configDeviceSetCmd}
	cmd.AddCommand(configDeviceUnsetCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdConfigDeviceAdd struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Short = i18n.G("Add instance devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add instance devices`))
	if c.config != nil {
		cmd.Use = usage("add", i18n.G("[<remote>:]<instance> <device> <type> [key=value...]"))
		cmd.Example = cli.FormatSection("", i18n.G(
			`incus config device add [<remote>:]instance1 <device-name> disk source=/share/c1 path=/opt
    Will mount the host's /share/c1 onto /opt in the instance.

incus config device add [<remote>:]instance1 <device-name> disk pool=some-pool source=some-volume path=/opt
    Will mount the some-volume volume on some-pool onto /opt in the instance.`))
	} else if c.profile != nil {
		cmd.Use = usage("add", i18n.G("[<remote>:]<profile> <device> <type> [key=value...]"))
		cmd.Example = cli.FormatSection("", i18n.G(
			`incus profile device add [<remote>:]profile1 <device-name> disk source=/share/c1 path=/opt
    Will mount the host's /share/c1 onto /opt in the instance.

incus profile device add [<remote>:]profile1 <device-name> disk pool=some-pool source=some-volume path=/opt
    Will mount the some-volume volume on some-pool onto /opt in the instance.`))
	}

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Add the device
	devname := args[1]
	device := map[string]string{}
	device["type"] = args[2]
	if len(args) > 3 {
		for _, prop := range args[3:] {
			results := strings.SplitN(prop, "=", 2)
			if len(results) != 2 {
				return fmt.Errorf(i18n.G("No value found in %q"), prop)
			}

			k := results[0]
			v := results[1]
			device[k] = v
		}
	}

	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		if profile.Devices == nil {
			profile.Devices = make(map[string]map[string]string)
		}

		_, ok := profile.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		profile.Devices[devname] = device

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		_, ok := inst.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		inst.Devices[devname] = device

		op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s added to %s")+"\n", devname, resource.name)
	}

	return nil
}

// Get.
type cmdConfigDeviceGet struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("get", i18n.G("[<remote>:]<instance> <device> <key>"))
	} else if c.profile != nil {
		cmd.Use = usage("get", i18n.G("[<remote>:]<profile> <device> <key>"))
	}

	cmd.Short = i18n.G("Get values for device configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for device configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		if len(args) == 1 {
			if c.config != nil {
				return c.global.cmpInstanceDeviceNames(args[0])
			} else if c.profile != nil {
				return c.global.cmpProfileDeviceNames(args[0])
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Get the config key
	devname := args[1]
	key := args[2]

	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("Device doesn't exist"))
		}

		fmt.Println(dev[key])
	} else {
		inst, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		dev, ok := inst.Devices[devname]
		if !ok {
			_, ok = inst.ExpandedDevices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("Device doesn't exist"))
			}

			return fmt.Errorf(i18n.G("Device from profile(s) cannot be retrieved for individual instance"))
		}

		fmt.Println(dev[key])
	}

	return nil
}

// List.
type cmdConfigDeviceList struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instance devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List instance devices`))
	if c.config != nil {
		cmd.Use = usage("list", i18n.G("[<remote>:]<instance>"))
	} else if c.profile != nil {
		cmd.Use = usage("list", i18n.G("[<remote>:]<profile>"))
	}

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// List the devices
	var devices []string
	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		for k := range profile.Devices {
			devices = append(devices, k)
		}
	} else {
		inst, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		for k := range inst.Devices {
			devices = append(devices, k)
		}
	}

	fmt.Printf("%s\n", strings.Join(devices, "\n"))

	return nil
}

// Override.
type cmdConfigDeviceOverride struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceOverride) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("override", i18n.G("[<remote>:]<instance> <device> [key=value...]"))
	cmd.Short = i18n.G("Copy profile inherited devices and override configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profile inherited devices and override configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceOverride) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Override the device
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	devname := args[1]
	_, ok := inst.Devices[devname]
	if ok {
		return fmt.Errorf(i18n.G("The device already exists"))
	}

	device, ok := inst.ExpandedDevices[devname]
	if !ok {
		return fmt.Errorf(i18n.G("The profile device doesn't exist"))
	}

	if len(args) > 2 {
		for _, prop := range args[2:] {
			results := strings.SplitN(prop, "=", 2)
			if len(results) != 2 {
				return fmt.Errorf(i18n.G("No value found in %q"), prop)
			}

			k := results[0]
			v := results[1]
			device[k] = v
		}
	}

	inst.Devices[devname] = device

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s overridden for %s")+"\n", devname, resource.name)
	}

	return nil
}

// Remove.
type cmdConfigDeviceRemove struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("remove", i18n.G("[<remote>:]<instance> <name>..."))
	} else if c.profile != nil {
		cmd.Use = usage("remove", i18n.G("[<remote>:]<profile> <name>..."))
	}

	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove instance devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove instance devices`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		if c.config != nil {
			return c.global.cmpInstanceDeviceNames(args[0])
		} else if c.profile != nil {
			return c.global.cmpProfileDeviceNames(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Remove the device
	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		for _, devname := range args[1:] {
			_, ok := profile.Devices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("Device doesn't exist"))
			}

			delete(profile.Devices, devname)
		}

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		for _, devname := range args[1:] {
			_, ok := inst.Devices[devname]
			if !ok {
				_, ok := inst.ExpandedDevices[devname]
				if !ok {
					return fmt.Errorf(i18n.G("Device doesn't exist"))
				}

				return fmt.Errorf(i18n.G("Device from profile(s) cannot be removed from individual instance. Override device or modify profile instead"))
			}

			delete(inst.Devices, devname)
		}

		op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s removed from %s")+"\n", strings.Join(args[1:], ", "), resource.name)
	}

	return nil
}

// Set.
type cmdConfigDeviceSet struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Short = i18n.G("Set device configuration keys")
	if c.config != nil {
		cmd.Use = usage("set", i18n.G("[<remote>:]<instance> <device> <key>=<value>..."))
		cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
			`Set device configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus config device set [<remote>:]<instance> <device> <key> <value>`))
	} else if c.profile != nil {
		cmd.Use = usage("set", i18n.G("[<remote>:]<profile> <device> <key>=<value>..."))
		cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
			`Set device configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus profile device set [<remote>:]<profile> <device> <key> <value>`))
	}

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		if len(args) == 1 {
			if c.config != nil {
				return c.global.cmpInstanceDeviceNames(args[0])
			} else if c.profile != nil {
				return c.global.cmpProfileDeviceNames(args[0])
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Set the device config key
	devname := args[1]

	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("Device doesn't exist"))
		}

		for k, v := range keys {
			dev[k] = v
		}

		profile.Devices[devname] = dev

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		dev, ok := inst.Devices[devname]
		if !ok {
			_, ok = inst.ExpandedDevices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("Device doesn't exist"))
			}

			return fmt.Errorf(i18n.G("Device from profile(s) cannot be modified for individual instance. Override device or modify profile instead"))
		}

		for k, v := range keys {
			dev[k] = v
		}

		inst.Devices[devname] = dev

		op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

// Show.
type cmdConfigDeviceShow struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("show", i18n.G("[<remote>:]<instance>"))
	} else if c.profile != nil {
		cmd.Use = usage("show", i18n.G("[<remote>:]<profile>"))
	}

	cmd.Short = i18n.G("Show full device configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show full device configuration`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Show the devices
	var devices map[string]map[string]string
	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		devices = profile.Devices
	} else {
		inst, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		devices = inst.Devices
	}

	data, err := yaml.Marshal(&devices)
	if err != nil {
		return err
	}

	fmt.Print(string(data))

	return nil
}

// Unset.
type cmdConfigDeviceUnset struct {
	global          *cmdGlobal
	config          *cmdConfig
	configDevice    *cmdConfigDevice
	configDeviceSet *cmdConfigDeviceSet
	profile         *cmdProfile
}

func (c *cmdConfigDeviceUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("unset", i18n.G("[<remote>:]<instance> <device> <key>"))
	} else if c.profile != nil {
		cmd.Use = usage("unset", i18n.G("[<remote>:]<profile> <device> <key>"))
	}

	cmd.Short = i18n.G("Unset device configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset device configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if c.config != nil {
				return c.global.cmpInstances(toComplete)
			} else if c.profile != nil {
				return c.global.cmpProfiles(toComplete, true)
			}
		}

		if len(args) == 1 {
			if c.config != nil {
				return c.global.cmpInstanceDeviceNames(args[0])
			} else if c.profile != nil {
				return c.global.cmpProfileDeviceNames(args[0])
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigDeviceUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	args = append(args, "")
	return c.configDeviceSet.Run(cmd, args)
}
