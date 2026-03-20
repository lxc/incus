package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdConfigDevice struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigDevice) formatUsage(usage u.Usage) u.Usage {
	if c.profile != nil {
		return append(u.Usage{u.Profile.Remote()}, usage...)
	}

	return append(u.Usage{u.Instance.Remote()}, usage...)
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDevice) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("device")
	cmd.Short = i18n.G("Manage devices")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage devices`))

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
		configDeviceOverrideCmd := cmdConfigDeviceOverride{global: c.global, config: c.config}
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
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdConfigDeviceAdd struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

var cmdConfigDeviceAddUsage = u.Usage{u.NewName(u.Device), u.Type, u.LegacyKV.List(0)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Aliases = []string{"create"}
	cmd.Use = cli.U("add", c.configDevice.formatUsage(cmdConfigDeviceAddUsage)...)
	cmd.Short = i18n.G("Add instance devices")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Add instance devices`))
	if c.config != nil {
		cmd.Example = cli.FormatSection("", i18n.G(
			`incus config device add [<remote>:]instance1 <device-name> disk source=/share/c1 path=/opt
    Will mount the host's /share/c1 onto /opt in the instance.

incus config device add [<remote>:]instance1 <device-name> disk pool=some-pool source=some-volume path=/opt
    Will mount the some-volume volume on some-pool onto /opt in the instance.`))
	} else if c.profile != nil {
		cmd.Example = cli.FormatSection("", i18n.G(
			`incus profile device add [<remote>:]profile1 <device-name> disk source=/share/c1 path=/opt
    Will mount the host's /share/c1 onto /opt in the instance.

incus profile device add [<remote>:]profile1 <device-name> disk pool=some-pool source=some-volume path=/opt
    Will mount the some-volume volume on some-pool onto /opt in the instance.`))
	}

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceAdd) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceAddUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String

	// Add the device
	devName := parsed[1].String
	device, err := kvToMap(parsed[3])
	if err != nil {
		return err
	}

	device["type"] = parsed[2].String

	if c.profile != nil {
		profile, etag, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		if profile.Devices == nil {
			profile.Devices = make(map[string]map[string]string)
		}

		_, ok := profile.Devices[devName]
		if ok {
			return errors.New(i18n.G("The device already exists"))
		}

		profile.Devices[devName] = device

		err = d.UpdateProfile(objectName, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := d.GetInstance(objectName)
		if err != nil {
			return err
		}

		_, ok := inst.Devices[devName]
		if ok {
			return errors.New(i18n.G("The device already exists"))
		}

		inst.Devices[devName] = device

		op, err := d.UpdateInstance(objectName, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s added to %s")+"\n", devName, objectName)
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

var cmdConfigDeviceGetUsage = u.Usage{u.Device, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", c.configDevice.formatUsage(cmdConfigDeviceGetUsage)...)
	cmd.Short = i18n.G("Get values for device configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for device configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceGet) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceGetUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String
	devName := parsed[1].String
	key := parsed[2].String

	if c.profile != nil {
		profile, _, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devName]
		if !ok {
			return errors.New(i18n.G("Device doesn't exist"))
		}

		fmt.Println(dev[key])
	} else {
		inst, _, err := d.GetInstance(objectName)
		if err != nil {
			return err
		}

		dev, ok := inst.Devices[devName]
		if !ok {
			_, ok = inst.ExpandedDevices[devName]
			if !ok {
				return errors.New(i18n.G("Device doesn't exist"))
			}

			return errors.New(i18n.G("Device from profile(s) cannot be retrieved for individual instance"))
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

var cmdConfigDeviceListUsage = u.Usage{}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", c.configDevice.formatUsage(cmdConfigDeviceListUsage)...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instance devices")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List instance devices`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceList) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceListUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String

	// List the devices
	var devices []string
	if c.profile != nil {
		profile, _, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		for k := range profile.Devices {
			devices = append(devices, k)
		}
	} else {
		inst, _, err := d.GetInstance(objectName)
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
	global *cmdGlobal
	config *cmdConfig
}

var cmdConfigDeviceOverrideUsage = u.Usage{u.Instance.Remote(), u.Device, u.LegacyKV.List(0)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceOverride) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("override", cmdConfigDeviceOverrideUsage...)
	cmd.Short = i18n.G("Copy profile inherited devices and override configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Copy profile inherited devices and override configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdConfigDeviceOverride) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigDeviceOverrideUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String
	devName := parsed[1].String

	// Override the device
	inst, etag, err := d.GetInstance(objectName)
	if err != nil {
		return err
	}

	_, ok := inst.Devices[devName]
	if ok {
		return errors.New(i18n.G("The device already exists"))
	}

	device, ok := inst.ExpandedDevices[devName]
	if !ok {
		return errors.New(i18n.G("The profile device doesn't exist"))
	}

	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	maps.Copy(device, keys)
	inst.Devices[devName] = device

	op, err := d.UpdateInstance(objectName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s overridden for %s")+"\n", devName, objectName)
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

var cmdConfigDeviceRemoveUsage = u.Usage{u.Device.List(1)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", c.configDevice.formatUsage(cmdConfigDeviceRemoveUsage)...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove instance devices")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove instance devices`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceRemove) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceRemoveUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String

	// Remove the device
	if c.profile != nil {
		profile, etag, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		var errs []error
		for _, p := range parsed[1].List {
			devName := p.String
			_, ok := profile.Devices[devName]
			if ok {
				delete(profile.Devices, devName)
			} else {
				errs = append(errs, fmt.Errorf(i18n.G("Device “%s” doesn't exist"), devName))
			}
		}

		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		err = d.UpdateProfile(objectName, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := d.GetInstance(objectName)
		if err != nil {
			return err
		}

		var errs []error
		for _, p := range parsed[1].List {
			devName := p.String
			_, ok := inst.Devices[devName]
			if ok {
				delete(inst.Devices, devName)
			} else {
				_, ok := inst.ExpandedDevices[devName]
				if !ok {
					errs = append(errs, fmt.Errorf(i18n.G("Device “%s” doesn't exist"), devName))
				}

				errs = append(errs, fmt.Errorf(i18n.G("Device from profile(s) cannot be removed from individual instance. Override device “%s” or modify profile instead"), devName))
			}
		}

		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		op, err := d.UpdateInstance(objectName, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s removed from %s")+"\n", strings.Join(parsed[1].StringList, ", "), objectName)
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

var cmdConfigDeviceSetUsage = u.Usage{u.Device, u.LegacyKV.List(1)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", c.configDevice.formatUsage(cmdConfigDeviceSetUsage)...)
	cmd.Short = i18n.G("Set device configuration keys")
	if c.config != nil {
		cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
			`Set device configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus config device set [<remote>:]<instance> <device> <key> <value>`))
	} else if c.profile != nil {
		cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
			`Set device configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus profile device set [<remote>:]<profile> <device> <key> <value>`))
	}

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// set runs the post-parsing command logic.
func (c *cmdConfigDeviceSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String
	devName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	if c.profile != nil {
		profile, etag, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devName]
		if !ok {
			return errors.New(i18n.G("Device doesn't exist"))
		}

		maps.Copy(dev, keys)
		profile.Devices[devName] = dev

		err = d.UpdateProfile(objectName, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := d.GetInstance(objectName)
		if err != nil {
			return err
		}

		dev, ok := inst.Devices[devName]
		if !ok {
			_, ok = inst.ExpandedDevices[devName]
			if !ok {
				return errors.New(i18n.G("Device doesn't exist"))
			}

			return errors.New(i18n.G("Device from profile(s) cannot be modified for individual instance. Override device or modify profile instead"))
		}

		maps.Copy(dev, keys)
		inst.Devices[devName] = dev

		op, err := d.UpdateInstance(objectName, inst.Writable(), etag)
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceSet) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceSetUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdConfigDeviceShow struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

var cmdConfigDeviceShowUsage = u.Usage{}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", c.configDevice.formatUsage(cmdConfigDeviceShowUsage)...)
	cmd.Short = i18n.G("Show full device configuration")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show full device configuration`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceShow) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceShowUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	objectName := parsed[0].RemoteObject.String

	// Show the devices
	var devices map[string]map[string]string
	if c.profile != nil {
		profile, _, err := d.GetProfile(objectName)
		if err != nil {
			return err
		}

		devices = profile.Devices
	} else {
		inst, _, err := d.GetInstance(objectName)
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

var cmdConfigDeviceUnsetUsage = u.Usage{u.Device, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdConfigDeviceUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", c.configDevice.formatUsage(cmdConfigDeviceUnsetUsage)...)
	cmd.Short = i18n.G("Unset device configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Unset device configuration keys`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

// Run runs the actual command logic.
func (c *cmdConfigDeviceUnset) Run(cmd *cobra.Command, args []string) error {
	parsed, err := c.configDevice.formatUsage(cmdConfigDeviceUnsetUsage).Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return unsetKey(c.configDeviceSet, cmd, parsed)
}
