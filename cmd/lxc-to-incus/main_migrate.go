package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	liblxc "github.com/lxc/go-lxc"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/util"
)

type cmdMigrate struct {
	global *cmdGlobal

	// Flags
	flagDryRun     bool
	flagDebug      bool
	flagAll        bool
	flagDelete     bool
	flagStorage    string
	flagLXCPath    string
	flagRsyncArgs  string
	flagContainers []string
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxc-to-incus",
		Short: i18n.G("Command line client for container migration"),
	}

	// Wrappers
	cmd.RunE = c.RunE

	// Flags
	cmd.Flags().BoolVar(&c.flagDryRun, "dry-run", false, i18n.G("Dry run mode"))
	cmd.Flags().BoolVar(&c.flagDebug, "debug", false, i18n.G("Print debugging output"))
	cmd.Flags().BoolVar(&c.flagAll, "all", false, i18n.G("Import all containers"))
	cmd.Flags().BoolVar(&c.flagDelete, "delete", false, i18n.G("Delete the source container"))
	cmd.Flags().StringVar(&c.flagStorage, "storage", "",
		i18n.G("Storage pool to use for the container")+"``")
	cmd.Flags().StringVar(&c.flagLXCPath, "lxcpath", liblxc.DefaultConfigPath(),
		i18n.G("Alternate LXC path")+"``")
	cmd.Flags().StringVar(&c.flagRsyncArgs, "rsync-args", "",
		"Extra arguments to pass to rsync"+"``")
	cmd.Flags().StringSliceVar(&c.flagContainers, "containers", nil,
		i18n.G("Container(s) to import")+"``")

	return cmd
}

func (c *cmdMigrate) RunE(cmd *cobra.Command, args []string) error {
	if (len(c.flagContainers) == 0 && !c.flagAll) || (len(c.flagContainers) > 0 && c.flagAll) {
		fmt.Fprintln(os.Stderr, "You must either pass container names or --all")
		os.Exit(1)
	}

	// Connect to the daemon
	d, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return err
	}

	// Retrieve LXC containers
	for _, container := range liblxc.Containers(c.flagLXCPath) {
		if !c.flagAll && !slices.Contains(c.flagContainers, container.Name()) {
			continue
		}

		err := convertContainer(d, container, c.flagStorage,
			c.flagDryRun, c.flagRsyncArgs, c.flagDebug)
		if err != nil {
			fmt.Printf("Skipping container '%s': %v\n", container.Name(), err)
			continue
		}

		// Delete container
		if c.flagDelete {
			if c.flagDryRun {
				fmt.Println("Would destroy container now")
				continue
			}

			err := container.Destroy()
			if err != nil {
				fmt.Printf("Failed to destroy container '%s': %v\n", container.Name(), err)
			}
		}
	}

	return nil
}

func validateConfig(conf []string, container *liblxc.Container) error {
	// Checking whether container has already been migrated
	fmt.Println("Checking whether container has already been migrated")
	if len(getConfig(conf, "incus.migrated")) > 0 {
		return fmt.Errorf("Container has already been migrated")
	}

	// Validate lxc.utsname / lxc.uts.name
	value := getConfig(conf, "lxc.uts.name")
	if value == nil {
		value = getConfig(conf, "lxc.utsname")
	}

	if value == nil || value[0] != container.Name() {
		return fmt.Errorf("Container name doesn't match lxc.uts.name / lxc.utsname")
	}

	// Validate lxc.aa_allow_incomplete: must be set to 0 or unset.
	fmt.Println("Validating whether incomplete AppArmor support is enabled")
	value = getConfig(conf, "lxc.apparmor.allow_incomplete")
	if value == nil {
		value = getConfig(conf, "lxc.aa_allow_incomplete")
	}

	if value != nil {
		v, err := strconv.Atoi(value[0])
		if err != nil {
			return err
		}

		if v != 0 {
			return fmt.Errorf("Container allows incomplete AppArmor support")
		}
	}

	// Validate lxc.autodev: must be set to 1 or unset.
	fmt.Println("Validating whether mounting a minimal /dev is enabled")
	value = getConfig(conf, "lxc.autodev")
	if value != nil {
		v, err := strconv.Atoi(value[0])
		if err != nil {
			return err
		}

		if v != 1 {
			return fmt.Errorf("Container doesn't mount a minimal /dev filesystem")
		}
	}

	// Extract and valid rootfs key
	fmt.Println("Validating container rootfs")
	rootfs, err := getRootfs(conf)
	if err != nil {
		return err
	}

	if !util.PathExists(rootfs) {
		return fmt.Errorf("Couldn't find the container rootfs '%s'", rootfs)
	}

	return nil
}

func convertContainer(d incus.InstanceServer, container *liblxc.Container, storage string,
	dryRun bool, rsyncArgs string, debug bool) error {
	// Don't migrate running containers
	if container.Running() {
		return fmt.Errorf("Only stopped containers can be migrated")
	}

	fmt.Println("Parsing LXC configuration")
	conf, err := parseConfig(container.ConfigFileName())
	if err != nil {
		return err
	}

	if debug {
		fmt.Printf("Container configuration:\n  %v\n", strings.Join(conf, "\n  "))
	}

	// Check whether there are unsupported keys in the config
	fmt.Println("Checking for unsupported LXC configuration keys")
	keys := getUnsupportedKeys(getConfigKeys(conf))
	for _, k := range keys {
		if !strings.HasPrefix(k, "lxc.net.") &&
			!strings.HasPrefix(k, "lxc.network.") &&
			!strings.HasPrefix(k, "lxc.cgroup.") &&
			!strings.HasPrefix(k, "lxc.cgroup2.") {
			return fmt.Errorf("Found unsupported config key '%s'", k)
		}
	}

	// Make sure we don't have a conflict
	fmt.Println("Checking for existing containers")
	containers, err := d.GetInstanceNames(api.InstanceTypeContainer)
	if err != nil {
		return err
	}

	found := false
	for _, name := range containers {
		if name == container.Name() {
			found = true
		}
	}
	if found {
		return fmt.Errorf("Container already exists")
	}

	// Validate config
	err = validateConfig(conf, container)
	if err != nil {
		return err
	}

	newConfig := make(map[string]string)

	value := getConfig(conf, "lxc.idmap")
	if value == nil {
		value = getConfig(conf, "lxc.id_map")
	}

	if value == nil {
		// Privileged container
		newConfig["security.privileged"] = "true"
	} else {
		// Unprivileged container
		newConfig["security.privileged"] = "false"
	}

	newDevices := make(map[string]map[string]string)

	// Convert network configuration
	err = convertNetworkConfig(container, newDevices)
	if err != nil {
		return err
	}

	// Convert storage configuration
	err = convertStorageConfig(conf, newDevices)
	if err != nil {
		return err
	}

	// Convert environment
	fmt.Println("Processing environment configuration")
	value = getConfig(conf, "lxc.environment")
	for _, env := range value {
		entry := strings.Split(env, "=")
		key := strings.TrimSpace(entry[0])
		val := strings.TrimSpace(entry[len(entry)-1])
		newConfig[fmt.Sprintf("environment.%s", key)] = val
	}

	// Convert auto-start
	fmt.Println("Processing container boot configuration")
	value = getConfig(conf, "lxc.start.auto")
	if value != nil {
		val, err := strconv.Atoi(value[0])
		if err != nil {
			return err
		}

		if val > 0 {
			newConfig["boot.autostart"] = "true"
		}
	}

	value = getConfig(conf, "lxc.start.delay")
	if value != nil {
		val, err := strconv.Atoi(value[0])
		if err != nil {
			return err
		}

		if val > 0 {
			newConfig["boot.autostart.delay"] = value[0]
		}
	}

	value = getConfig(conf, "lxc.start.order")
	if value != nil {
		val, err := strconv.Atoi(value[0])
		if err != nil {
			return err
		}

		if val > 0 {
			newConfig["boot.autostart.priority"] = value[0]
		}
	}

	// Convert apparmor
	fmt.Println("Processing container apparmor configuration")
	value = getConfig(conf, "lxc.apparmor.profile")
	if value == nil {
		value = getConfig(conf, "lxc.aa_profile")
	}

	if value != nil {
		if value[0] == "lxc-container-default-with-nesting" {
			newConfig["security.nesting"] = "true"
		} else if value[0] != "lxc-container-default" {
			newConfig["raw.lxc"] = fmt.Sprintf("lxc.apparmor.profile=%s\n", value[0])
		}
	}

	// Convert seccomp
	fmt.Println("Processing container seccomp configuration")
	value = getConfig(conf, "lxc.seccomp.profile")
	if value == nil {
		value = getConfig(conf, "lxc.seccomp")
	}

	if value != nil && value[0] != "/usr/share/lxc/config/common.seccomp" {
		return fmt.Errorf("Custom seccomp profiles aren't supported")
	}

	// Convert SELinux
	fmt.Println("Processing container SELinux configuration")
	value = getConfig(conf, "lxc.selinux.context")
	if value == nil {
		value = getConfig(conf, "lxc.se_context")
	}

	if value != nil {
		return fmt.Errorf("Custom SELinux policies aren't supported")
	}

	// Convert capabilities
	fmt.Println("Processing container capabilities configuration")
	value = getConfig(conf, "lxc.cap.drop")
	if value != nil {
		for _, cap := range strings.Split(value[0], " ") {
			// Ignore capabilities that are dropped in containers by default.
			if slices.Contains([]string{"mac_admin", "mac_override", "sys_module",
				"sys_time"}, cap) {
				continue
			}

			return fmt.Errorf("Custom capabilities aren't supported")
		}
	}

	value = getConfig(conf, "lxc.cap.keep")
	if value != nil {
		return fmt.Errorf("Custom capabilities aren't supported")
	}

	// Add rest of the keys to lxc.raw
	for _, c := range conf {
		parts := strings.SplitN(c, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "lxc.signal.halt", "lxc.haltsignal":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.signal.halt=%s\n", val)
		case "lxc.signal.reboot", "lxc.rebootsignal":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.signal.reboot=%s\n", val)
		case "lxc.signal.stop", "lxc.stopsignal":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.signal.stop=%s\n", val)
		case "lxc.apparmor.allow_incomplete", "lxc.aa_allow_incomplete":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.apparmor.allow_incomplete=%s\n", val)
		case "lxc.pty.max", "lxc.pts":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.pty.max=%s\n", val)
		case "lxc.tty.max", "lxc.tty":
			newConfig["raw.lxc"] += fmt.Sprintf("lxc.tty.max=%s\n", val)
		}
	}

	// Setup the container creation request
	req := api.InstancesPost{
		Name: container.Name(),
		Source: api.InstanceSource{
			Type: "migration",
			Mode: "push",
		},
	}

	req.Config = newConfig
	req.Devices = newDevices
	req.Profiles = []string{"default"}

	// Set the container architecture if set in LXC
	fmt.Println("Processing container architecture configuration")
	var arch string
	value = getConfig(conf, "lxc.arch")
	if value == nil {
		fmt.Println("Couldn't find container architecture, assuming native")
		arch = runtime.GOARCH
	} else {
		arch = value[0]
	}

	archID, err := osarch.ArchitectureId(arch)
	if err != nil {
		// If arch is linux32 or linux64, the architecture ID cannot be determined as multiple
		// architectures have the linux32 or linux64 personality. In this case, assume the native
		// architecture.
		arch = runtime.GOARCH

		archID, err = osarch.ArchitectureId(arch)
		if err != nil {
			return err
		}

		// If the instance architecture is 32bit but the local architecture is 64bit, iterate
		// through the local architecture's personalities until the supported architecture
		// personality matches the instance's architecture.
		if len(value) > 0 && value[0] == "linux32" {
			personalities, err := osarch.ArchitecturePersonalities(archID)
			if err != nil {
				return err
			}

			for id, personality := range personalities {
				arch, err = osarch.ArchitecturePersonality(personality)
				if err != nil {
					return err
				}

				if arch == value[0] {
					archID = id
					break
				}
			}
		}
	}

	req.Architecture, err = osarch.ArchitectureName(archID)
	if err != nil {
		return err
	}

	if storage != "" {
		req.Devices["root"] = map[string]string{
			"type": "disk",
			"pool": storage,
			"path": "/",
		}
	}

	if debug {
		out, _ := json.MarshalIndent(req, "", "  ")
		fmt.Printf("Container config:\n%v\n", string(out))
	}

	// Create container
	fmt.Println("Creating container")
	if dryRun {
		fmt.Println("Would create container now")
	} else {
		op, err := d.CreateInstance(req)
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{Format: "Transferring container: %s"}
		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		rootfs, _ := getRootfs(conf)

		err = transferRootfs(d, op, rootfs, rsyncArgs)
		if err != nil {
			return err
		}

		progress.Done(fmt.Sprintf("Container '%s' successfully created", container.Name()))
	}

	return nil
}

func convertNetworkConfig(container *liblxc.Container, devices map[string]map[string]string) error {
	networkDevice := func(network map[string]string) map[string]string {
		if network == nil {
			return nil
		}

		device := make(map[string]string)
		device["type"] = "nic"

		// Get the device type
		device["nictype"] = network["type"]

		// Convert the configuration
		for k, v := range network {
			switch k {
			case "hwaddr", "mtu", "name":
				device[k] = v
			case "link":
				device["parent"] = v
			case "veth_pair":
				device["host_name"] = v
			case "":
				// empty key
				return nil
			}
		}

		switch device["nictype"] {
		case "veth":
			_, ok := device["parent"]
			if ok {
				device["nictype"] = "bridged"
			} else {
				device["nictype"] = "p2p"
			}

		case "phys":
			device["nictype"] = "physical"
		case "empty":
			return nil
		}

		return device
	}

	fmt.Println("Processing network configuration")

	devices["eth0"] = make(map[string]string)
	devices["eth0"]["type"] = "none"

	// New config key
	for i := range container.ConfigItem("lxc.net") {
		network := networkGet(container, i, "lxc.net")

		dev := networkDevice(network)
		if dev == nil {
			continue
		}

		devices[fmt.Sprintf("net%d", i)] = dev
	}

	// Old config key
	for i := range container.ConfigItem("lxc.network") {
		network := networkGet(container, i, "lxc.network")

		dev := networkDevice(network)
		if dev == nil {
			continue
		}

		devices[fmt.Sprintf("net%d", len(devices))] = dev
	}

	return nil
}

func convertStorageConfig(conf []string, devices map[string]map[string]string) error {
	fmt.Println("Processing storage configuration")

	i := 0
	for _, mount := range getConfig(conf, "lxc.mount.entry") {
		parts := strings.Split(mount, " ")
		if len(parts) < 4 {
			return fmt.Errorf("Invalid mount configuration: %s", mount)
		}

		// Ignore mounts that are present in containers by default.
		if slices.Contains([]string{"proc", "sysfs"}, parts[0]) {
			continue
		}

		device := make(map[string]string)
		device["type"] = "disk"

		// Deal with read-only mounts
		if slices.Contains(strings.Split(parts[3], ","), "ro") {
			device["readonly"] = "true"
		}

		// Deal with optional mounts
		if slices.Contains(strings.Split(parts[3], ","), "optional") {
			device["optional"] = "true"
		} else {
			if strings.HasPrefix(parts[0], "/") {
				if !util.PathExists(parts[0]) {
					return fmt.Errorf("Invalid path: %s", parts[0])
				}
			} else {
				continue
			}
		}

		// Set the source
		device["source"] = parts[0]

		// Figure out the target
		if !strings.HasPrefix(parts[1], "/") {
			device["path"] = fmt.Sprintf("/%s", parts[1])
		} else {
			rootfs, err := getRootfs(conf)
			if err != nil {
				return err
			}

			device["path"] = strings.TrimPrefix(parts[1], rootfs)
		}

		devices[fmt.Sprintf("mount%d", i)] = device
		i++
	}

	return nil
}

func getRootfs(conf []string) (string, error) {
	value := getConfig(conf, "lxc.rootfs.path")
	if value == nil {
		value = getConfig(conf, "lxc.rootfs")
		if value == nil {
			return "", fmt.Errorf("Invalid container, missing lxc.rootfs key")
		}
	}

	// Get the rootfs path
	parts := strings.SplitN(value[0], ":", 2)

	return parts[len(parts)-1], nil
}
