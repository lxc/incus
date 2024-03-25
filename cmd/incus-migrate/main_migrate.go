package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
)

type cmdMigrate struct {
	global *cmdGlobal

	flagRsyncArgs string
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "incus-migrate"
	cmd.Short = "Physical to instance migration tool"
	cmd.Long = `Description:
  Physical to instance migration tool

  This tool lets you turn any Linux filesystem (including your current one)
  into an instance on a remote host.

  It will setup a clean mount tree made of the root filesystem and any
  additional mount you list, then transfer this through the migration
  API to create a new instance from it.

  The same set of options as ` + "`incus launch`" + ` are also supported.
`
	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagRsyncArgs, "rsync-args", "", "Extra arguments to pass to rsync"+"``")

	return cmd
}

type cmdMigrateData struct {
	SourcePath   string
	Mounts       []string
	InstanceArgs api.InstancesPost
	Project      string
}

func (c *cmdMigrateData) Render() string {
	data := struct {
		Name        string            `yaml:"Name"`
		Project     string            `yaml:"Project"`
		Type        api.InstanceType  `yaml:"Type"`
		Source      string            `yaml:"Source"`
		Mounts      []string          `yaml:"Mounts,omitempty"`
		Profiles    []string          `yaml:"Profiles,omitempty"`
		StoragePool string            `yaml:"Storage pool,omitempty"`
		StorageSize string            `yaml:"Storage pool size,omitempty"`
		Network     string            `yaml:"Network name,omitempty"`
		Config      map[string]string `yaml:"Config,omitempty"`
	}{
		c.InstanceArgs.Name,
		c.Project,
		c.InstanceArgs.Type,
		c.SourcePath,
		c.Mounts,
		c.InstanceArgs.Profiles,
		"",
		"",
		"",
		c.InstanceArgs.Config,
	}

	disk, ok := c.InstanceArgs.Devices["root"]
	if ok {
		data.StoragePool = disk["pool"]

		size, ok := disk["size"]
		if ok {
			data.StorageSize = size
		}
	}

	network, ok := c.InstanceArgs.Devices["eth0"]
	if ok {
		data.Network = network["parent"]
	}

	out, err := yaml.Marshal(&data)
	if err != nil {
		return ""
	}

	return string(out)
}

func (c *cmdMigrate) askServer() (incus.InstanceServer, string, error) {
	// Detect local server.
	local, err := c.connectLocal()
	if err == nil {
		useLocal, err := c.global.asker.AskBool("The local Incus server is the target [default=yes]: ", "yes")
		if err != nil {
			return nil, "", err
		}

		if useLocal {
			return local, "", nil
		}
	}

	// Server address
	serverURL, err := c.global.asker.AskString("Please provide Incus server URL: ", "", nil)
	if err != nil {
		return nil, "", err
	}

	serverURL, err = parseURL(serverURL)
	if err != nil {
		return nil, "", err
	}

	args := incus.ConnectionArgs{
		UserAgent:          fmt.Sprintf("LXC-MIGRATE %s", version.Version),
		InsecureSkipVerify: true,
	}

	certificate, err := localtls.GetRemoteCertificate(serverURL, args.UserAgent)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to get remote certificate: %w", err)
	}

	digest := localtls.CertFingerprint(certificate)

	fmt.Println("Certificate fingerprint:", digest)
	fmt.Print("ok (y/n)? ")

	buf := bufio.NewReader(os.Stdin)
	line, _, err := buf.ReadLine()
	if err != nil {
		return nil, "", err
	}

	if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
		return nil, "", fmt.Errorf("Server certificate rejected by user")
	}

	server, err := incus.ConnectIncus(serverURL, &args)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to connect to server: %w", err)
	}

	apiServer, _, err := server.GetServer()
	if err != nil {
		return nil, "", fmt.Errorf("Failed to get server: %w", err)
	}

	fmt.Println("")

	type AuthMethod int

	const (
		authMethodTLSCertificate AuthMethod = iota
		authMethodTLSTemporaryCertificate
		authMethodTLSCertificateToken
	)

	// TLS is always available
	var availableAuthMethods []AuthMethod
	var authMethod AuthMethod

	i := 1

	if slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
		fmt.Printf("%d) Use a certificate token\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificateToken)
		i++
		fmt.Printf("%d) Use an existing TLS authentication certificate\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificate)
		i++
		fmt.Printf("%d) Generate a temporary TLS authentication certificate\n", i)
		availableAuthMethods = append(availableAuthMethods, authMethodTLSTemporaryCertificate)
	}

	if len(apiServer.AuthMethods) > 1 || slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
		authMethodInt, err := c.global.asker.AskInt("Please pick an authentication mechanism above: ", 1, int64(i), "", nil)
		if err != nil {
			return nil, "", err
		}

		authMethod = availableAuthMethods[authMethodInt-1]
	}

	var certPath string
	var keyPath string
	var token string

	if authMethod == authMethodTLSCertificate {
		certPath, err = c.global.asker.AskString("Please provide the certificate path: ", "", func(path string) error {
			if !util.PathExists(path) {
				return errors.New("File does not exist")
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}

		keyPath, err = c.global.asker.AskString("Please provide the keyfile path: ", "", func(path string) error {
			if !util.PathExists(path) {
				return errors.New("File does not exist")
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}
	} else if authMethod == authMethodTLSCertificateToken {
		token, err = c.global.asker.AskString("Please provide the certificate token: ", "", func(token string) error {
			_, err := localtls.CertificateTokenDecode(token)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}

	var authType string

	switch authMethod {
	case authMethodTLSCertificate, authMethodTLSTemporaryCertificate, authMethodTLSCertificateToken:
		authType = api.AuthenticationMethodTLS
	}

	return c.connectTarget(serverURL, certPath, keyPath, authType, token)
}

func (c *cmdMigrate) RunInteractive(server incus.InstanceServer) (cmdMigrateData, error) {
	var err error

	config := cmdMigrateData{}

	config.InstanceArgs = api.InstancesPost{
		Source: api.InstanceSource{
			Type: "migration",
			Mode: "push",
		},
	}

	config.InstanceArgs.Config = map[string]string{}
	config.InstanceArgs.Devices = map[string]map[string]string{}

	// Provide instance type
	instanceType, err := c.global.asker.AskInt("Would you like to create a container (1) or virtual-machine (2)?: ", 1, 2, "1", nil)
	if err != nil {
		return cmdMigrateData{}, err
	}

	if instanceType == 1 {
		config.InstanceArgs.Type = api.InstanceTypeContainer
	} else if instanceType == 2 {
		config.InstanceArgs.Type = api.InstanceTypeVM
	}

	// Project
	projectNames, err := server.GetProjectNames()
	if err != nil {
		return cmdMigrateData{}, err
	}

	if len(projectNames) > 1 {
		project, err := c.global.asker.AskChoice("Project to create the instance in [default=default]: ", projectNames, api.ProjectDefaultName)
		if err != nil {
			return cmdMigrateData{}, err
		}

		config.Project = project
	} else {
		config.Project = api.ProjectDefaultName
	}

	// Instance name
	instanceNames, err := server.GetInstanceNames(api.InstanceTypeAny)
	if err != nil {
		return cmdMigrateData{}, err
	}

	for {
		instanceName, err := c.global.asker.AskString("Name of the new instance: ", "", nil)
		if err != nil {
			return cmdMigrateData{}, err
		}

		if slices.Contains(instanceNames, instanceName) {
			fmt.Printf("Instance %q already exists\n", instanceName)
			continue
		}

		config.InstanceArgs.Name = instanceName
		break
	}

	var question string

	// Provide source path
	if config.InstanceArgs.Type == api.InstanceTypeVM {
		question = "Please provide the path to a disk, partition, or raw image file: "
	} else {
		question = "Please provide the path to a root filesystem: "
	}

	config.SourcePath, err = c.global.asker.AskString(question, "", func(s string) error {
		if !util.PathExists(s) {
			return errors.New("Path does not exist")
		}

		_, err := os.Stat(s)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return cmdMigrateData{}, err
	}

	if config.InstanceArgs.Type == api.InstanceTypeVM {
		architectureName, _ := osarch.ArchitectureGetLocal()

		if slices.Contains([]string{"x86_64", "aarch64"}, architectureName) {
			hasUEFI, err := c.global.asker.AskBool("Does the VM support UEFI booting? [default=yes]: ", "yes")
			if err != nil {
				return cmdMigrateData{}, err
			}

			if hasUEFI {
				hasSecureBoot, err := c.global.asker.AskBool("Does the VM support UEFI Secure Boot? [default=yes]: ", "yes")
				if err != nil {
					return cmdMigrateData{}, err
				}

				if !hasSecureBoot {
					config.InstanceArgs.Config["security.secureboot"] = "false"
				}
			} else {
				config.InstanceArgs.Config["security.csm"] = "true"
				config.InstanceArgs.Config["security.secureboot"] = "false"
			}
		}
	}

	var mounts []string

	// Additional mounts for containers
	if config.InstanceArgs.Type == api.InstanceTypeContainer {
		addMounts, err := c.global.asker.AskBool("Do you want to add additional filesystem mounts? [default=no]: ", "no")
		if err != nil {
			return cmdMigrateData{}, err
		}

		if addMounts {
			for {
				path, err := c.global.asker.AskString("Please provide a path the filesystem mount path [empty value to continue]: ", "", func(s string) error {
					if s != "" {
						if util.PathExists(s) {
							return nil
						}

						return errors.New("Path does not exist")
					}

					return nil
				})
				if err != nil {
					return cmdMigrateData{}, err
				}

				if path == "" {
					break
				}

				mounts = append(mounts, path)
			}

			config.Mounts = append(config.Mounts, mounts...)
		}
	}

	for {
		fmt.Println("\nInstance to be created:")

		scanner := bufio.NewScanner(strings.NewReader(config.Render()))
		for scanner.Scan() {
			fmt.Printf("  %s\n", scanner.Text())
		}

		fmt.Print(`
Additional overrides can be applied at this stage:
1) Begin the migration with the above configuration
2) Override profile list
3) Set additional configuration options
4) Change instance storage pool or volume size
5) Change instance network

`)

		choice, err := c.global.asker.AskInt("Please pick one of the options above [default=1]: ", 1, 5, "1", nil)
		if err != nil {
			return cmdMigrateData{}, err
		}

		switch choice {
		case 1:
			return config, nil
		case 2:
			err = c.askProfiles(server, &config)
		case 3:
			err = c.askConfig(&config)
		case 4:
			err = c.askStorage(server, &config)
		case 5:
			err = c.askNetwork(server, &config)
		}

		if err != nil {
			fmt.Println(err)
		}
	}
}

func (c *cmdMigrate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	_, err := exec.LookPath("rsync")
	if err != nil {
		return err
	}

	// Server
	server, clientFingerprint, err := c.askServer()
	if err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-sigChan

		if clientFingerprint != "" {
			_ = server.DeleteCertificate(clientFingerprint)
		}

		cancel()
		os.Exit(1)
	}()

	if clientFingerprint != "" {
		defer func() { _ = server.DeleteCertificate(clientFingerprint) }()
	}

	config, err := c.RunInteractive(server)
	if err != nil {
		return err
	}

	if config.Project != "" {
		server = server.UseProject(config.Project)
	}

	config.Mounts = append(config.Mounts, config.SourcePath)

	// Get and sort the mounts
	sort.Strings(config.Mounts)

	// Create the mount namespace and ensure we're not moved around
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Unshare a new mntns so our mounts don't leak
	err = unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		return fmt.Errorf("Failed to unshare mount namespace: %w", err)
	}

	// Prevent mount propagation back to initial namespace
	err = unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	if err != nil {
		return fmt.Errorf("Failed to disable mount propagation: %w", err)
	}

	// Create the temporary directory to be used for the mounts
	path, err := os.MkdirTemp("", "incus-migrate_mount_")
	if err != nil {
		return err
	}

	// Automatically clean-up the temporary path on exit
	defer func(path string) {
		_ = unix.Unmount(path, unix.MNT_DETACH)
		_ = os.Remove(path)
	}(path)

	var fullPath string

	if config.InstanceArgs.Type == api.InstanceTypeContainer {
		// Create the rootfs directory
		fullPath = fmt.Sprintf("%s/rootfs", path)

		err = os.Mkdir(fullPath, 0755)
		if err != nil {
			return err
		}

		// Setup the source (mounts)
		err = setupSource(fullPath, config.Mounts)
		if err != nil {
			return fmt.Errorf("Failed to setup the source: %w", err)
		}
	} else {
		fullPath = path
		target := filepath.Join(path, "root.img")

		err := os.WriteFile(target, nil, 0644)
		if err != nil {
			return fmt.Errorf("Failed to create %q: %w", target, err)
		}

		// Mount the path
		err = unix.Mount(config.SourcePath, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %w", config.SourcePath, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %w", config.SourcePath, err)
		}
	}

	// System architecture
	architectureName, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return err
	}

	config.InstanceArgs.Architecture = architectureName

	revert := revert.New()
	defer revert.Fail()

	// Create the instance
	op, err := server.CreateInstance(config.InstanceArgs)
	if err != nil {
		return err
	}

	revert.Add(func() {
		_, _ = server.DeleteInstance(config.InstanceArgs.Name)
	})

	progress := cli.ProgressRenderer{Format: "Transferring instance: %s"}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = transferRootfs(ctx, server, op, fullPath, c.flagRsyncArgs, config.InstanceArgs.Type)
	if err != nil {
		return err
	}

	progress.Done(fmt.Sprintf("Instance %s successfully created", config.InstanceArgs.Name))
	revert.Success()

	return nil
}

func (c *cmdMigrate) askProfiles(server incus.InstanceServer, config *cmdMigrateData) error {
	profileNames, err := server.GetProfileNames()
	if err != nil {
		return err
	}

	profiles, err := c.global.asker.AskString("Which profiles do you want to apply to the instance? (space separated) [default=default, \"-\" for none]: ", "default", func(s string) error {
		// This indicates that no profiles should be applied.
		if s == "-" {
			return nil
		}

		profiles := strings.Split(s, " ")

		for _, profile := range profiles {
			if !slices.Contains(profileNames, profile) {
				return fmt.Errorf("Unknown profile %q", profile)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if profiles != "-" {
		config.InstanceArgs.Profiles = strings.Split(profiles, " ")
	}

	return nil
}

func (c *cmdMigrate) askConfig(config *cmdMigrateData) error {
	configs, err := c.global.asker.AskString("Please specify config keys and values (key=value ...): ", "", func(s string) error {
		if s == "" {
			return nil
		}

		for _, entry := range strings.Split(s, " ") {
			if !strings.Contains(entry, "=") {
				return fmt.Errorf("Bad key=value configuration: %v", entry)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, entry := range strings.Split(configs, " ") {
		key, value, _ := strings.Cut(entry, "=")
		config.InstanceArgs.Config[key] = value
	}

	return nil
}

func (c *cmdMigrate) askStorage(server incus.InstanceServer, config *cmdMigrateData) error {
	storagePools, err := server.GetStoragePoolNames()
	if err != nil {
		return err
	}

	if len(storagePools) == 0 {
		return fmt.Errorf("No storage pools available")
	}

	storagePool, err := c.global.asker.AskChoice("Please provide the storage pool to use: ", storagePools, "")
	if err != nil {
		return err
	}

	config.InstanceArgs.Devices["root"] = map[string]string{
		"type": "disk",
		"pool": storagePool,
		"path": "/",
	}

	changeStorageSize, err := c.global.asker.AskBool("Do you want to change the storage size? [default=no]: ", "no")
	if err != nil {
		return err
	}

	if changeStorageSize {
		size, err := c.global.asker.AskString("Please specify the storage size: ", "", func(s string) error {
			_, err := units.ParseByteSizeString(s)
			return err
		})
		if err != nil {
			return err
		}

		config.InstanceArgs.Devices["root"]["size"] = size
	}

	return nil
}

func (c *cmdMigrate) askNetwork(server incus.InstanceServer, config *cmdMigrateData) error {
	networks, err := server.GetNetworkNames()
	if err != nil {
		return err
	}

	network, err := c.global.asker.AskChoice("Please specify the network to use for the instance: ", networks, "")
	if err != nil {
		return err
	}

	config.InstanceArgs.Devices["eth0"] = map[string]string{
		"type":    "nic",
		"nictype": "bridged",
		"parent":  network,
		"name":    "eth0",
	}

	return nil
}
