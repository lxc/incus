package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/units"
)

type cmdNetwork struct {
	global *cmdGlobal

	flagTarget string
	flagType   string
}

type networkColumn struct {
	Name string
	Data func(api.Network) string
}

func (c *cmdNetwork) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("network")
	cmd.Short = i18n.G("Manage and attach instances to networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Manage and attach instances to networks`))

	// Attach
	networkAttachCmd := cmdNetworkAttach{global: c.global, network: c}
	cmd.AddCommand(networkAttachCmd.command())

	// Attach profile
	networkAttachProfileCmd := cmdNetworkAttachProfile{global: c.global, network: c}
	cmd.AddCommand(networkAttachProfileCmd.command())

	// Create
	networkCreateCmd := cmdNetworkCreate{global: c.global, network: c}
	cmd.AddCommand(networkCreateCmd.command())

	// Delete
	networkDeleteCmd := cmdNetworkDelete{global: c.global, network: c}
	cmd.AddCommand(networkDeleteCmd.command())

	// Detach
	networkDetachCmd := cmdNetworkDetach{global: c.global, network: c}
	cmd.AddCommand(networkDetachCmd.command())

	// Detach profile
	networkDetachProfileCmd := cmdNetworkDetachProfile{global: c.global, network: c, networkDetach: &networkDetachCmd}
	cmd.AddCommand(networkDetachProfileCmd.command())

	// Edit
	networkEditCmd := cmdNetworkEdit{global: c.global, network: c}
	cmd.AddCommand(networkEditCmd.command())

	// Get
	networkGetCmd := cmdNetworkGet{global: c.global, network: c}
	cmd.AddCommand(networkGetCmd.command())

	// Info
	networkInfoCmd := cmdNetworkInfo{global: c.global, network: c}
	cmd.AddCommand(networkInfoCmd.command())

	// List
	networkListCmd := cmdNetworkList{global: c.global, network: c}
	cmd.AddCommand(networkListCmd.command())

	// List allocations
	networkListAllocationsCmd := cmdNetworkListAllocations{global: c.global, network: c}
	cmd.AddCommand(networkListAllocationsCmd.command())

	// List leases
	networkListLeasesCmd := cmdNetworkListLeases{global: c.global, network: c}
	cmd.AddCommand(networkListLeasesCmd.command())

	// Rename
	networkRenameCmd := cmdNetworkRename{global: c.global, network: c}
	cmd.AddCommand(networkRenameCmd.command())

	// Set
	networkSetCmd := cmdNetworkSet{global: c.global, network: c}
	cmd.AddCommand(networkSetCmd.command())

	// Show
	networkShowCmd := cmdNetworkShow{global: c.global, network: c}
	cmd.AddCommand(networkShowCmd.command())

	// Unset
	networkUnsetCmd := cmdNetworkUnset{global: c.global, network: c, networkSet: &networkSetCmd}
	cmd.AddCommand(networkUnsetCmd.command())

	// ACL
	networkACLCmd := cmdNetworkACL{global: c.global}
	cmd.AddCommand(networkACLCmd.command())

	// Address set
	networkAddressSetCmd := cmdNetworkAddressSet{global: c.global}
	cmd.AddCommand(networkAddressSetCmd.command())

	// Forward
	networkForwardCmd := cmdNetworkForward{global: c.global}
	cmd.AddCommand(networkForwardCmd.command())

	// Integration
	networkIntegrationCmd := cmdNetworkIntegration{global: c.global}
	cmd.AddCommand(networkIntegrationCmd.command())

	// Load Balancer
	networkLoadBalancerCmd := cmdNetworkLoadBalancer{global: c.global}
	cmd.AddCommand(networkLoadBalancerCmd.command())

	// Peer
	networkPeerCmd := cmdNetworkPeer{global: c.global}
	cmd.AddCommand(networkPeerCmd.command())

	// Zone
	networkZoneCmd := cmdNetworkZone{global: c.global}
	cmd.AddCommand(networkZoneCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Attach.
type cmdNetworkAttach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkAttachUsage = u.Usage{u.Network.Remote(), u.Instance, u.NewName(u.Device).Optional(u.NewName(u.Interface).Optional())}

func (c *cmdNetworkAttach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("attach", cmdNetworkAttachUsage...)
	cmd.Short = i18n.G("Attach network interfaces to instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Attach new network interfaces to instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstances(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAttach) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAttachUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	instanceName := parsed[1].String
	hasDevice := !parsed[2].Skipped
	deviceName := networkName
	interfaceName := ""

	if hasDevice {
		deviceName = parsed[2].List[0].String
		interfaceName = parsed[2].List[1].String
	}

	// Get the network entry
	network, _, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	// Prepare the instance's device entry
	var device map[string]string
	if network.Managed && d.HasExtension("instance_nic_network") {
		// If network is managed, use the network property rather than nictype, so that the network's
		// inherited properties are loaded into the NIC when started.
		device = map[string]string{
			"type":    "nic",
			"network": network.Name,
		}
	} else {
		// If network is unmanaged default to using a macvlan connected to the specified interface.
		device = map[string]string{
			"type":    "nic",
			"nictype": "macvlan",
			"parent":  networkName,
		}

		if network.Type == "bridge" {
			// If the network type is an unmanaged bridge, use bridged NIC type.
			device["nictype"] = "bridged"
		}
	}

	device["name"] = interfaceName

	// Add the device to the instance
	err = instanceDeviceAdd(d, instanceName, deviceName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile.
type cmdNetworkAttachProfile struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkAttachProfileUsage = u.Usage{u.Network.Remote(), u.Profile, u.NewName(u.Device).Optional(u.NewName(u.Interface).Optional())}

func (c *cmdNetworkAttachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("attach-profile", cmdNetworkAttachProfileUsage...)
	cmd.Short = i18n.G("Attach network interfaces to profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Attach network interfaces to profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(args[0], false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAttachProfile) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkAttachProfileUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	profileName := parsed[1].String
	hasDevice := !parsed[2].Skipped
	deviceName := networkName
	interfaceName := ""

	if hasDevice {
		deviceName = parsed[2].List[0].String
		interfaceName = parsed[2].List[1].String
	}

	// Get the network entry
	network, _, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	// Prepare the instance's device entry
	var device map[string]string
	if network.Managed && d.HasExtension("instance_nic_network") {
		// If network is managed, use the network property rather than nictype, so that the network's
		// inherited properties are loaded into the NIC when started.
		device = map[string]string{
			"type":    "nic",
			"network": network.Name,
		}
	} else {
		// If network is unmanaged default to using a macvlan connected to the specified interface.
		device = map[string]string{
			"type":    "nic",
			"nictype": "macvlan",
			"parent":  networkName,
		}

		if network.Type == "bridge" {
			// If the network type is an unmanaged bridge, use bridged NIC type.
			device["nictype"] = "bridged"
		}
	}

	device["name"] = interfaceName

	// Add the device to the profile
	err = profileDeviceAdd(d, profileName, deviceName, device)
	if err != nil {
		return err
	}

	return nil
}

// Create.
type cmdNetworkCreate struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagDescription string
}

var cmdNetworkCreateUsage = u.Usage{u.NewName(u.Network).Remote(), u.KV.List(0)}

func (c *cmdNetworkCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdNetworkCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create new networks`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus network create foo
    Create a new network called foo

incus network create foo < config.yaml
    Create a new network called foo using the content of config.yaml.

incus network create bar network=baz --type ovn
    Create a new OVN network called bar using baz as its uplink network`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVarP(&c.network.flagType, "type", "t", "", i18n.G("Network type")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Network description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, false)
	}

	return cmd
}

func (c *cmdNetworkCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	var stdinData api.NetworkPut

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		err = loader.Load(&stdinData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	// Create the network
	network := api.NetworksPost{
		NetworkPut: stdinData,
	}

	network.Name = networkName
	network.Type = c.network.flagType

	if c.flagDescription != "" {
		network.Description = c.flagDescription
	}

	if network.Config == nil {
		network.Config = map[string]string{}
	}

	maps.Copy(network.Config, keys)

	// If a target member was specified the API won't actually create the
	// network, but only define it as pending in the database.
	if c.network.flagTarget != "" {
		d = d.UseTarget(c.network.flagTarget)
	}

	err = d.CreateNetwork(network)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if c.network.flagTarget != "" {
			fmt.Printf(i18n.G("Network %s pending on member %s")+"\n", formatRemote(c.global.conf, parsed[0]), c.network.flagTarget)
		} else {
			fmt.Printf(i18n.G("Network %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
		}
	}

	return nil
}

// Delete.
type cmdNetworkDelete struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkDeleteUsage = u.Usage{u.Network.Remote().List(1)}

func (c *cmdNetworkDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdNetworkDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete networks`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

func (c *cmdNetworkDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		networkName := p.RemoteObject.String

		// Delete the network
		err = d.DeleteNetwork(networkName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Network %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Detach.
type cmdNetworkDetach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkDetachUsage = u.Usage{u.Network.Remote(), u.Instance, u.Device.Optional()}

func (c *cmdNetworkDetach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("detach", cmdNetworkDetachUsage...)
	cmd.Short = i18n.G("Detach network interfaces from instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Detach network interfaces from instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkInstances(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Find a matching device.
func (c *cmdNetworkDetach) findDevice(devices map[string]map[string]string, networkName string, dev *u.Parsed) (string, error) {
	hasDevice := !dev.Skipped
	devName := dev.String
	found := false
	for n, d := range devices {
		if hasDevice {
			if n == devName {
				if d["type"] != "nic" {
					return "", fmt.Errorf(i18n.G("The specified device is not a NIC (%s device)"), d["type"])
				}

				if d["parent"] != networkName && d["network"] != networkName {
					return "", fmt.Errorf(i18n.G("The specified NIC does not point to the given network (found %s)"), d["parent"]+d["network"])
				}

				found = true
				break
			}

			continue
		}

		if d["type"] == "nic" && (d["parent"] == networkName || d["network"] == networkName) {
			if found {
				return "", errors.New(i18n.G("More than one device matches, specify the device name"))
			}

			devName = n
			found = true
		}
	}

	if !found {
		return "", errors.New(i18n.G("No device found for this network"))
	}

	return devName, nil
}

func (c *cmdNetworkDetach) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkDetachUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	instanceName := parsed[1].String

	// Get the instance entry
	inst, etag, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	deviceName, err := c.findDevice(inst.Devices, networkName, parsed[2])
	if err != nil {
		return err
	}

	// Remove the device
	delete(inst.Devices, deviceName)
	op, err := d.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile.
type cmdNetworkDetachProfile struct {
	global        *cmdGlobal
	network       *cmdNetwork
	networkDetach *cmdNetworkDetach
}

var cmdNetworkDetachProfileUsage = u.Usage{u.Network.Remote(), u.Profile, u.Device.Optional()}

func (c *cmdNetworkDetachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("detach-profile", cmdNetworkDetachProfileUsage...)
	cmd.Short = i18n.G("Detach network interfaces from profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Detach network interfaces from profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkProfiles(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkDetachProfile) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkDetachProfileUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	profileName := parsed[1].String

	// Get the profile entry
	profile, etag, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	deviceName, err := c.networkDetach.findDevice(profile.Devices, networkName, parsed[2])
	if err != nil {
		return err
	}

	// Remove the device
	delete(profile.Devices, deviceName)
	err = d.UpdateProfile(profileName, profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Edit.
type cmdNetworkEdit struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkEditUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdNetworkEditUsage...)
	cmd.Short = i18n.G("Edit network configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit network configurations as YAML`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

func (c *cmdNetworkEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network.
### Any line starting with a '# will be ignored.
###
### A network consists of a set of configuration items.
###
### An example would look like:
### name: mybr0
### config:
###   ipv4.address: 10.62.42.1/24
###   ipv4.nat: true
###   ipv6.address: fd00:56ad:9f7a:9800::1/64
###   ipv6.nat: true
### managed: true
### type: bridge
###
### Note that only the configuration can be changed.`)
}

func (c *cmdNetworkEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkPut{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateNetwork(networkName, newdata, "")
	}

	// Extract the current value
	network, etag, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	if !network.Managed {
		return errors.New(i18n.G("Only managed networks can be modified"))
	}

	data, err := yaml.Dump(&network, yaml.V2)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.NetworkPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateNetwork(networkName, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = cli.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdNetworkGet struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagIsProperty bool
}

var cmdNetworkGetUsage = u.Usage{u.Network.Remote(), u.Key}

func (c *cmdNetworkGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdNetworkGetUsage...)
	cmd.Short = i18n.G("Get values for network configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for network configuration keys`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the network key
	if c.network.flagTarget != "" {
		d = d.UseTarget(c.network.flagTarget)
	}

	resp, _, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range resp.Config {
			if k == key {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Info.
type cmdNetworkInfo struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkInfoUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdNetworkInfoUsage...)
	cmd.Short = i18n.G("Get runtime information on networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get runtime information on networks`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

func (c *cmdNetworkInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	// Targeting.
	if c.network.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.network.flagTarget)
	}

	state, err := d.GetNetworkState(networkName)
	if err != nil {
		return err
	}

	// Interface information.
	fmt.Printf(i18n.G("Name: %s")+"\n", networkName)

	if state.Hwaddr != "" {
		fmt.Printf(i18n.G("MAC address: %s")+"\n", state.Hwaddr)
	}

	fmt.Printf(i18n.G("MTU: %d")+"\n", state.Mtu)
	fmt.Printf(i18n.G("State: %s")+"\n", state.State)
	fmt.Printf(i18n.G("Type: %s")+"\n", state.Type)

	// IP addresses.
	if len(state.Addresses) > 0 {
		fmt.Println("")
		fmt.Println(i18n.G("IP addresses:"))
		for _, addr := range state.Addresses {
			fmt.Printf("  %s\t%s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope)
		}
	}

	// Network usage.
	if state.Counters != nil {
		fmt.Println("")
		fmt.Println(i18n.G("Network usage:"))
		fmt.Printf("  %s: %s\n", i18n.G("Bytes received"), units.GetByteSizeString(state.Counters.BytesReceived, 2))
		fmt.Printf("  %s: %s\n", i18n.G("Bytes sent"), units.GetByteSizeString(state.Counters.BytesSent, 2))
		fmt.Printf("  %s: %d\n", i18n.G("Packets received"), state.Counters.PacketsReceived)
		fmt.Printf("  %s: %d\n", i18n.G("Packets sent"), state.Counters.PacketsSent)
	}

	// Bond information.
	if state.Bond != nil {
		fmt.Println("")
		fmt.Println(i18n.G("Bond:"))
		fmt.Printf("  %s: %s\n", i18n.G("Mode"), state.Bond.Mode)
		fmt.Printf("  %s: %s\n", i18n.G("Transmit policy"), state.Bond.TransmitPolicy)
		fmt.Printf("  %s: %d\n", i18n.G("Up delay"), state.Bond.UpDelay)
		fmt.Printf("  %s: %d\n", i18n.G("Down delay"), state.Bond.DownDelay)
		fmt.Printf("  %s: %d\n", i18n.G("MII Frequency"), state.Bond.MIIFrequency)
		fmt.Printf("  %s: %s\n", i18n.G("MII state"), state.Bond.MIIState)
		fmt.Printf("  %s: %s\n", i18n.G("Lower devices"), strings.Join(state.Bond.LowerDevices, ", "))
	}

	// Bridge information.
	if state.Bridge != nil {
		fmt.Println("")
		fmt.Println(i18n.G("Bridge:"))
		fmt.Printf("  %s: %s\n", i18n.G("ID"), state.Bridge.ID)
		fmt.Printf("  %s: %v\n", i18n.G("STP"), state.Bridge.STP)
		fmt.Printf("  %s: %d\n", i18n.G("Forward delay"), state.Bridge.ForwardDelay)
		fmt.Printf("  %s: %d\n", i18n.G("Default VLAN ID"), state.Bridge.VLANDefault)
		fmt.Printf("  %s: %v\n", i18n.G("VLAN filtering"), state.Bridge.VLANFiltering)
		fmt.Printf("  %s: %s\n", i18n.G("Upper devices"), strings.Join(state.Bridge.UpperDevices, ", "))
	}

	// VLAN information.
	if state.VLAN != nil {
		fmt.Println("")
		fmt.Println(i18n.G("VLAN:"))
		fmt.Printf("  %s: %s\n", i18n.G("Lower device"), state.VLAN.LowerDevice)
		fmt.Printf("  %s: %d\n", i18n.G("VLAN ID"), state.VLAN.VID)
	}

	// OVN information.
	if state.OVN != nil {
		fmt.Println("")
		fmt.Println(i18n.G("OVN:"))

		if state.OVN.Chassis != "" {
			fmt.Printf("  %s: %s\n", i18n.G("Chassis"), state.OVN.Chassis)
		}

		if state.OVN.LogicalRouter != "" {
			fmt.Printf("  %s: %s\n", i18n.G("Logical router"), state.OVN.LogicalRouter)
		}

		if state.OVN.LogicalSwitch != "" {
			fmt.Printf("  %s: %s\n", i18n.G("Logical switch"), state.OVN.LogicalSwitch)
		}

		if state.OVN.UplinkIPv4 != "" {
			fmt.Printf("  %s: %s\n", i18n.G("IPv4 uplink address"), state.OVN.UplinkIPv4)
		}

		if state.OVN.UplinkIPv6 != "" {
			fmt.Printf("  %s: %s\n", i18n.G("IPv6 uplink address"), state.OVN.UplinkIPv6)
		}
	}

	return nil
}

// List.
type cmdNetworkList struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

var cmdNetworkListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdNetworkList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdNetworkListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available networks

Filters may be of the <key>=<value> form for property based filtering,
or part of the network name. Filters must be delimited by a ','.

Examples:
  - "foo" lists all networks that start with the name foo
  - "name=foo" lists all networks that exactly have the name foo
  - "type=bridge" lists all networks with the type bridge

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Default column layout is: ntm46dus
Column shorthand chars:
4 - IPv4 address
6 - IPv6 address
d - Description
e - Project name
m - Managed status
n - Network Interface Name
s - State
t - Interface type
u - Used by (count)`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List networks in all projects"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, false)
	}

	return cmd
}

const defaultNetworkColumns = "ntm46dus"

func (c *cmdNetworkList) parseColumns() ([]networkColumn, error) {
	columnsShorthandMap := map[rune]networkColumn{
		'e': {i18n.G("PROJECT"), c.projectColumnData},
		'n': {i18n.G("NAME"), c.networkNameColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'm': {i18n.G("MANAGED"), c.managedColumnData},
		'4': {i18n.G("IPV4"), c.ipv4ColumnData},
		'6': {i18n.G("IPV6"), c.ipv6ColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
		's': {i18n.G("STATE"), c.stateColumnData},
	}

	if c.flagColumns == defaultNetworkColumns && c.flagAllProjects {
		c.flagColumns = "entm46dus"
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []networkColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdNetworkList) networkNameColumnData(network api.Network) string {
	return network.Name
}

func (c *cmdNetworkList) typeColumnData(network api.Network) string {
	return network.Type
}

func (c *cmdNetworkList) managedColumnData(network api.Network) string {
	if network.Managed {
		return i18n.G("YES")
	}

	return i18n.G("NO")
}

func (c *cmdNetworkList) projectColumnData(network api.Network) string {
	return network.Project
}

func (c *cmdNetworkList) ipv4ColumnData(network api.Network) string {
	return network.Config["ipv4.address"]
}

func (c *cmdNetworkList) ipv6ColumnData(network api.Network) string {
	return network.Config["ipv6.address"]
}

func (c *cmdNetworkList) descriptionColumnData(network api.Network) string {
	return network.Description
}

func (c *cmdNetworkList) usedByColumnData(network api.Network) string {
	return fmt.Sprintf("%d", len(network.UsedBy))
}

func (c *cmdNetworkList) stateColumnData(network api.Network) string {
	return strings.ToUpper(network.Status)
}

func (c *cmdNetworkList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	filters := parsed[1].StringList

	filters = prepareNetworkServerFilters(filters)
	serverFilters, _ := getServerSupportedFilters(filters, []string{}, false)

	var networks []api.Network
	if c.flagAllProjects {
		networks, err = d.GetNetworksAllProjectsWithFilter(serverFilters)
	} else {
		networks, err = d.GetNetworksWithFilter(serverFilters)
	}

	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, network := range networks {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(network))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, networks)
}

// List leases.
type cmdNetworkListLeases struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat  string
	flagColumns string
}

type networkLeasesColumn struct {
	Name string
	Data func(api.NetworkLease) string
}

var cmdNetworkListLeasesUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkListLeases) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list-leases", cmdNetworkListLeasesUsage...)
	cmd.Short = i18n.G("List DHCP leases")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List DHCP leases

Default column layout: hmitL

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  h - Hostname
  m - MAC Address
  i - IP Address
  t - Type
  L - Location of the DHCP Lease (e.g. its cluster member)`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultNetworkListLeasesColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

const defaultNetworkListLeasesColumns = "hmit"

func (c *cmdNetworkListLeases) parseColumns(clustered bool) ([]networkLeasesColumn, error) {
	columnsShorthandMap := map[rune]networkLeasesColumn{
		'h': {i18n.G("HOSTNAME"), c.hostnameColumnData},
		'm': {i18n.G("MAC ADDRESS"), c.macAddressColumnData},
		'i': {i18n.G("IP ADDRESS"), c.ipAddressColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []networkLeasesColumn{}
	if c.flagColumns == defaultNetworkListLeasesColumns && clustered {
		columnList = append(columnList, "L")
	}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdNetworkListLeases) hostnameColumnData(lease api.NetworkLease) string {
	return lease.Hostname
}

func (c *cmdNetworkListLeases) macAddressColumnData(lease api.NetworkLease) string {
	return lease.Hwaddr
}

func (c *cmdNetworkListLeases) ipAddressColumnData(lease api.NetworkLease) string {
	return lease.Address
}

func (c *cmdNetworkListLeases) typeColumnData(lease api.NetworkLease) string {
	return strings.ToUpper(lease.Type)
}

func (c *cmdNetworkListLeases) locationColumnData(lease api.NetworkLease) string {
	return lease.Location
}

func (c *cmdNetworkListLeases) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkListLeasesUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	// List DHCP leases
	leases, err := d.GetNetworkLeases(networkName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, lease := range leases {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(lease))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, leases)
}

// Rename.
type cmdNetworkRename struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkRenameUsage = u.Usage{u.Network.Remote(), u.NewName(u.Network)}

func (c *cmdNetworkRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdNetworkRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename networks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename networks`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

func (c *cmdNetworkRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	newNetworkName := parsed[1].String

	// Rename the network
	err = d.RenameNetwork(networkName, api.NetworkPost{Name: newNetworkName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newNetworkName)
	}

	return nil
}

// Set.
type cmdNetworkSet struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagIsProperty bool
}

var cmdNetworkSetUsage = u.Usage{u.Network.Remote(), u.LegacyKV.List(1)}

func (c *cmdNetworkSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdNetworkSetUsage...)
	cmd.Short = i18n.G("Set network configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set network configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <key> <value>`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdNetworkSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Handle targeting
	if c.network.flagTarget != "" {
		d = d.UseTarget(c.network.flagTarget)
	}

	// Get the network
	network, etag, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	if !network.Managed {
		return errors.New(i18n.G("Only managed networks can be modified"))
	}

	writable := network.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return d.UpdateNetwork(networkName, writable, etag)
}

func (c *cmdNetworkSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdNetworkShow struct {
	global  *cmdGlobal
	network *cmdNetwork
}

var cmdNetworkShowUsage = u.Usage{u.Network.Remote()}

func (c *cmdNetworkShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdNetworkShowUsage...)
	cmd.Short = i18n.G("Show network configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show network configurations`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpNetworks(toComplete)
	}

	return cmd
}

func (c *cmdNetworkShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	networkName := parsed[0].RemoteObject.String

	// Show the network config
	if c.network.flagTarget != "" {
		d = d.UseTarget(c.network.flagTarget)
	}

	network, _, err := d.GetNetwork(networkName)
	if err != nil {
		return err
	}

	sort.Strings(network.UsedBy)

	data, err := yaml.Dump(&network, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdNetworkUnset struct {
	global     *cmdGlobal
	network    *cmdNetwork
	networkSet *cmdNetworkSet

	flagIsProperty bool
}

var cmdNetworkUnsetUsage = u.Usage{u.Network.Remote(), u.Key}

func (c *cmdNetworkUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdNetworkUnsetUsage...)
	cmd.Short = i18n.G("Unset network configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Unset network configuration keys`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdNetworkUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.networkSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.networkSet, cmd, parsed)
}

// prepareNetworkServerFilter processes and formats filter criteria
// for networks, ensuring they are in a format that the server can interpret.
func prepareNetworkServerFilters(filters []string) []string {
	flattenedFilters := []string{}
	for _, filter := range filters {
		flattenedFilters = append(flattenedFilters, strings.Split(filter, ",")...)
	}

	formattedFilters := []string{}

	for _, filter := range flattenedFilters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			filter = fmt.Sprintf("name=^%s($|.*)", regexp.QuoteMeta(key))
		} else if len(membs) == 2 {
			firstPart := key
			if strings.Contains(key, ".") {
				firstPart = strings.Split(key, ".")[0]
			}

			if !structHasField(reflect.TypeOf(api.Network{}), firstPart) {
				filter = fmt.Sprintf("config.%s", filter)
			}

			if key == "state" {
				filter = fmt.Sprintf("status=%s", membs[1])
			}
		}

		formattedFilters = append(formattedFilters, filter)
	}

	return formattedFilters
}
