package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdCreate struct {
	global *cmdGlobal

	flagConfig     []string
	flagDevice     []string
	flagEphemeral  bool
	flagNetwork    string
	flagProfile    []string
	flagStorage    string
	flagTarget     string
	flagType       string
	flagNoProfiles bool
	flagEmpty      bool
	flagVM         bool
}

func (c *cmdCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<image> [<remote>:][<name>]"))
	cmd.Short = i18n.G("Create instances from images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Create instances from images`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus create images:ubuntu/22.04 u1

incus create images:ubuntu/22.04 u1 < config.yaml
    Create the instance with configuration from config.yaml`))

	cmd.Aliases = []string{"init"}
	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new instance")+"``")
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, i18n.G("New key/value to apply to a specific device")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral instance"))
	cmd.Flags().StringVarP(&c.flagNetwork, "network", "n", "", i18n.G("Network name")+"``")
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "", i18n.G("Instance type")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the instance with no profiles applied"))
	cmd.Flags().BoolVar(&c.flagEmpty, "empty", false, i18n.G("Create an empty instance"))
	cmd.Flags().BoolVar(&c.flagVM, "vm", false, i18n.G("Create a virtual machine"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 2)
	if exit {
		return err
	}

	if len(args) == 0 && !c.flagEmpty {
		_ = cmd.Usage()
		return nil
	}

	_, _, err = c.create(c.global.conf, args, false)
	return err
}

func (c *cmdCreate) create(conf *config.Config, args []string, launch bool) (incus.InstanceServer, string, error) {
	var name string
	var image string
	var remote string
	var iremote string
	var err error
	var stdinData api.InstancePut
	var devicesMap map[string]map[string]string
	var configMap map[string]string
	var profiles []string

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, "", err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return nil, "", err
		}
	}

	if len(args) > 0 {
		iremote, image, err = conf.ParseRemote(args[0])
		if err != nil {
			return nil, "", err
		}

		if len(args) == 1 {
			remote, name, err = conf.ParseRemote("")
			if err != nil {
				return nil, "", err
			}
		} else if len(args) == 2 {
			remote, name, err = conf.ParseRemote(args[1])
			if err != nil {
				return nil, "", err
			}
		}
	}

	if c.flagEmpty {
		if len(args) > 1 {
			return nil, "", fmt.Errorf(i18n.G("--empty cannot be combined with an image name"))
		}

		if len(args) == 0 {
			remote, name, err = conf.ParseRemote("")
			if err != nil {
				return nil, "", err
			}
		} else if len(args) == 1 {
			// Switch image / instance names
			name = image
			remote = iremote
			image = ""
			iremote = ""
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return nil, "", err
	}

	// Overwrite profiles.
	if c.flagProfile != nil {
		profiles = c.flagProfile
	} else if c.flagNoProfiles {
		profiles = []string{}
	}

	if !c.global.flagQuiet {
		if d.HasExtension("instance_create_start") && launch {
			if name == "" {
				fmt.Printf(i18n.G("Launching the instance") + "\n")
			} else {
				fmt.Printf(i18n.G("Launching %s")+"\n", name)
			}
		} else {
			if name == "" {
				fmt.Printf(i18n.G("Creating the instance") + "\n")
			} else {
				fmt.Printf(i18n.G("Creating %s")+"\n", name)
			}
		}
	}

	if len(stdinData.Devices) > 0 {
		devicesMap = stdinData.Devices
	} else {
		devicesMap = map[string]map[string]string{}
	}

	if c.flagNetwork != "" {
		network, _, err := d.GetNetwork(c.flagNetwork)
		if err != nil {
			return nil, "", fmt.Errorf(i18n.G("Failed loading network %q: %w"), c.flagNetwork, err)
		}

		// Prepare the instance's NIC device entry.
		var device map[string]string

		if network.Managed && d.HasExtension("instance_nic_network") {
			// If network is managed, use the network property rather than nictype, so that the
			// network's inherited properties are loaded into the NIC when started.
			device = map[string]string{
				"name":    "eth0",
				"type":    "nic",
				"network": network.Name,
			}
		} else {
			// If network is unmanaged default to using a macvlan connected to the specified interface.
			device = map[string]string{
				"name":    "eth0",
				"type":    "nic",
				"nictype": "macvlan",
				"parent":  c.flagNetwork,
			}

			if network.Type == "bridge" {
				// If the network type is an unmanaged bridge, use bridged NIC type.
				device["nictype"] = "bridged"
			}
		}

		devicesMap["eth0"] = device
	}

	if len(stdinData.Config) > 0 {
		configMap = stdinData.Config
	} else {
		configMap = map[string]string{}
	}

	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, "", fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
		}

		configMap[key] = value
	}

	// Check if the specified storage pool exists.
	if c.flagStorage != "" {
		_, _, err := d.GetStoragePool(c.flagStorage)
		if err != nil {
			return nil, "", fmt.Errorf(i18n.G("Failed loading storage pool %q: %w"), c.flagStorage, err)
		}

		devicesMap["root"] = map[string]string{
			"type": "disk",
			"path": "/",
			"pool": c.flagStorage,
		}
	}

	// Decide whether we are creating a container or a virtual machine.
	instanceDBType := api.InstanceTypeContainer
	if c.flagVM {
		instanceDBType = api.InstanceTypeVM
	}

	// Set the target if provided.
	if c.flagTarget != "" {
		d = d.UseTarget(c.flagTarget)
	}

	// Setup instance creation request
	req := api.InstancesPost{
		Name:         name,
		InstanceType: c.flagType,
		Type:         instanceDBType,
		Start:        launch,
	}

	req.Config = configMap
	req.Ephemeral = c.flagEphemeral
	req.Description = stdinData.Description

	if !c.flagNoProfiles && len(profiles) == 0 {
		if len(stdinData.Profiles) > 0 {
			req.Profiles = stdinData.Profiles
		} else {
			req.Profiles = nil
		}
	} else {
		req.Profiles = profiles
	}

	// Handle device overrides.
	deviceOverrides, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return nil, "", err
	}

	// Check to see if any of the overridden devices are for devices that are not yet defined in the
	// local devices (and thus maybe expected to be coming from profiles).
	profileDevices := make(map[string]map[string]string)
	needProfileExpansion := false
	for deviceName := range deviceOverrides {
		_, isLocalDevice := devicesMap[deviceName]
		if !isLocalDevice {
			needProfileExpansion = true
			break
		}
	}

	// If there are device overrides that are expected to be applied to profile devices then load the profiles
	// that would be applied server-side.
	if needProfileExpansion {
		// If the list of profiles is empty then the default profile would be applied on the server side.
		serverSideProfiles := req.Profiles
		if len(serverSideProfiles) == 0 {
			serverSideProfiles = []string{"default"}
		}

		// Get the effective expanded devices by overlaying each profile's devices in order.
		for _, profileName := range serverSideProfiles {
			profile, _, err := d.GetProfile(profileName)
			if err != nil {
				return nil, "", fmt.Errorf(i18n.G("Failed loading profile %q for device override: %w"), profileName, err)
			}

			for k, v := range profile.Devices {
				profileDevices[k] = v
			}
		}
	}

	// Apply device overrides.
	for deviceName := range deviceOverrides {
		_, isLocalDevice := devicesMap[deviceName]
		if isLocalDevice {
			// Apply overrides to local device.
			for k, v := range deviceOverrides[deviceName] {
				devicesMap[deviceName][k] = v
			}
		} else {
			// Check device exists in expanded profile devices.
			profileDeviceConfig, found := profileDevices[deviceName]
			if !found {
				return nil, "", fmt.Errorf(i18n.G("Cannot override config for device %q: Device not found in profile devices"), deviceName)
			}

			for k, v := range deviceOverrides[deviceName] {
				profileDeviceConfig[k] = v
			}

			// Add device to local devices.
			devicesMap[deviceName] = profileDeviceConfig
		}
	}

	req.Devices = devicesMap

	var opInfo api.Operation
	if !c.flagEmpty {
		// Get the image server and image info
		iremote, image = guessImage(conf, d, remote, iremote, image)

		// Deal with the default image
		if image == "" {
			image = "default"
		}

		imgRemote, imgInfo, err := getImgInfo(d, conf, iremote, remote, image, &req.Source)
		if err != nil {
			return nil, "", err
		}

		if conf.Remotes[iremote].Protocol != "simplestreams" {
			if imgInfo.Type != "virtual-machine" && c.flagVM {
				return nil, "", fmt.Errorf(i18n.G("Asked for a VM but image is of type container"))
			}

			req.Type = api.InstanceType(imgInfo.Type)
		}

		// Create the instance
		op, err := d.CreateInstanceFromImage(imgRemote, *imgInfo, req)
		if err != nil {
			return nil, "", err
		}

		// Watch the background operation
		progress := cli.ProgressRenderer{
			Format: i18n.G("Retrieving image: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return nil, "", err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return nil, "", err
		}

		progress.Done("")

		// Extract the instance name
		info, err := op.GetTarget()
		if err != nil {
			return nil, "", err
		}

		opInfo = *info
	} else {
		req.Source.Type = "none"

		op, err := d.CreateInstance(req)
		if err != nil {
			return nil, "", err
		}

		err = op.Wait()
		if err != nil {
			return nil, "", err
		}

		opInfo = op.Get()
	}

	instances, ok := opInfo.Resources["instances"]
	if !ok || len(instances) == 0 {
		return nil, "", fmt.Errorf(i18n.G("Didn't get name of new instance from the server"))
	}

	if len(instances) == 1 && name == "" {
		url, err := url.Parse(instances[0])
		if err != nil {
			return nil, "", err
		}

		name = path.Base(url.Path)
		fmt.Printf(i18n.G("Instance name is: %s")+"\n", name)
	}

	// Validate the network setup
	c.checkNetwork(d, name)

	return d, name, nil
}

func (c *cmdCreate) checkNetwork(d incus.InstanceServer, name string) {
	ct, _, err := d.GetInstance(name)
	if err != nil {
		return
	}

	for _, d := range ct.ExpandedDevices {
		if d["type"] == "nic" {
			return
		}
	}

	fmt.Fprintf(os.Stderr, "\n"+i18n.G("The instance you are starting doesn't have any network attached to it.")+"\n")
	fmt.Fprintf(os.Stderr, "  "+i18n.G("To create a new network, use: incus network create")+"\n")
	fmt.Fprintf(os.Stderr, "  "+i18n.G("To attach a network to an instance, use: incus network attach")+"\n\n")
}
