package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ask"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

// InstanceMigration handles the migration logic for an instance.
type InstanceMigration struct {
	*Migration

	flagRsyncArgs string
	instanceArgs  api.InstancesPost
	volumes       []*VolumeMigration
}

// NewInstanceMigration returns a new InstanceMigration.
func NewInstanceMigration(ctx context.Context, server incus.InstanceServer, asker ask.Asker, flafRsyncArgs string, migraionType MigrationType) Migrator {
	return &InstanceMigration{
		Migration: &Migration{
			asker:         asker,
			ctx:           ctx,
			server:        server,
			migrationType: migraionType,
		},
		flagRsyncArgs: flafRsyncArgs,
	}
}

// gatherInfo collects information from the user about the instance to be created.
func (m *InstanceMigration) gatherInfo() error {
	var err error

	m.instanceArgs = api.InstancesPost{
		Source: api.InstanceSource{
			Type: "migration",
			Mode: "push",
		},
	}

	m.instanceArgs.Config = map[string]string{}
	m.instanceArgs.Devices = map[string]map[string]string{}

	if m.migrationType == MigrationTypeVM {
		m.instanceArgs.Type = api.InstanceTypeVM
	} else {
		m.instanceArgs.Type = api.InstanceTypeContainer
	}

	// Project
	err = m.askProject("Project to create the instance in [default=default]: ")
	if err != nil {
		return err
	}

	if m.project != "" {
		m.server = m.server.UseProject(m.project)
	}

	// Target
	err = m.askTarget()
	if err != nil {
		return err
	}

	m.server = m.server.UseTarget(m.target)

	// Instance name
	instanceNames, err := m.server.GetInstanceNames(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	for {
		instanceName, err := m.asker.AskString("Name of the new instance: ", "", nil)
		if err != nil {
			return err
		}

		if slices.Contains(instanceNames, instanceName) {
			fmt.Printf("Instance %q already exists\n", instanceName)
			continue
		}

		m.instanceArgs.Name = instanceName
		break
	}

	var question string
	// Provide source path
	if m.migrationType == MigrationTypeVM || m.migrationType == MigrationTypeVolumeBlock {
		question = "Please provide the path to a disk, partition, or qcow2/raw/vmdk image file: "
	} else {
		question = "Please provide the path to a root filesystem: "
	}

	// Provide source path
	err = m.askSourcePath(question)
	if err != nil {
		return err
	}

	err = m.setSourceFormat()
	if err != nil {
		return err
	}

	err = m.askUEFISupport()
	if err != nil {
		return err
	}

	var mounts []string

	// Additional mounts for containers
	if m.instanceArgs.Type == api.InstanceTypeContainer {
		addMounts, err := m.asker.AskBool("Do you want to add additional filesystem mounts? [default=no]: ", "no")
		if err != nil {
			return err
		}

		if addMounts {
			for {
				path, err := m.asker.AskString("Please provide a path the filesystem mount path [empty value to continue]: ", "", func(s string) error {
					if s != "" {
						if util.PathExists(s) {
							return nil
						}

						return errors.New("Path does not exist")
					}

					return nil
				})
				if err != nil {
					return err
				}

				if path == "" {
					break
				}

				mounts = append(mounts, path)
			}

			m.mounts = append(m.mounts, mounts...)
		}
	}

	return nil
}

// migrate performs the instance migration.
func (m *InstanceMigration) migrate() error {
	if m.migrationType != MigrationTypeVM && m.migrationType != MigrationTypeContainer {
		return errors.New("Wrong migration type for migrate")
	}

	// Prioritize migrating all additional disks before the main instance.
	for _, vol := range m.volumes {
		err := vol.migrate()
		if err != nil {
			return err
		}
	}

	return m.runMigration(func(path string) error {
		// System architecture
		architectureName, err := osarch.ArchitectureGetLocal()
		if err != nil {
			return err
		}

		m.instanceArgs.Architecture = architectureName

		reverter := revert.New()
		defer reverter.Fail()

		// Create the instance
		op, err := m.server.CreateInstance(m.instanceArgs)
		if err != nil {
			return err
		}

		reverter.Add(func() {
			_, _ = m.server.DeleteInstance(m.instanceArgs.Name)
		})

		progress := cli.ProgressRenderer{Format: "Transferring instance: %s"}
		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = transferRootfs(m.ctx, op, path, m.flagRsyncArgs, m.migrationType)
		if err != nil {
			return err
		}

		progress.Done(fmt.Sprintf("Instance %s successfully created", m.instanceArgs.Name))
		reverter.Success()

		return nil
	})
}

// renderObject renders the state of the instance.
func (m *InstanceMigration) renderObject() error {
	for {
		fmt.Println("\nInstance to be created:")

		scanner := bufio.NewScanner(strings.NewReader(m.render()))
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
6) Add additional disk
7) Change additional disk storage pool

`)

		choice, err := m.asker.AskInt("Please pick one of the options above [default=1]: ", 1, 6, "1", nil)
		if err != nil {
			return err
		}

		switch choice {
		case 1:
			return nil
		case 2:
			err = m.askProfiles()
		case 3:
			err = m.askConfig()
		case 4:
			err = m.askStorage()
		case 5:
			err = m.askNetwork()
		case 6:
			err = m.askDisk()
		case 7:
			err = m.askDiskStorage()
		}

		if err != nil {
			fmt.Println(err)
		}
	}
}

func (m *InstanceMigration) render() string {
	data := struct {
		Name         string                       `yaml:"Name"`
		Project      string                       `yaml:"Project"`
		Type         api.InstanceType             `yaml:"Type"`
		Source       string                       `yaml:"Source"`
		SourceFormat string                       `yaml:"Source format,omitempty"`
		Mounts       []string                     `yaml:"Mounts,omitempty"`
		Profiles     []string                     `yaml:"Profiles,omitempty"`
		StoragePool  string                       `yaml:"Storage pool,omitempty"`
		StorageSize  string                       `yaml:"Storage pool size,omitempty"`
		Network      string                       `yaml:"Network name,omitempty"`
		Config       map[string]string            `yaml:"Config,omitempty"`
		Disks        map[string]map[string]string `yaml:"Disks,omitempty"`
	}{
		m.instanceArgs.Name,
		m.project,
		m.instanceArgs.Type,
		m.sourcePath,
		m.sourceFormat,
		m.mounts,
		m.instanceArgs.Profiles,
		"",
		"",
		"",
		m.instanceArgs.Config,
		make(map[string]map[string]string),
	}

	disk, ok := m.instanceArgs.Devices["root"]
	if ok {
		data.StoragePool = disk["pool"]

		size, ok := disk["size"]
		if ok {
			data.StorageSize = size
		}
	}

	network, ok := m.instanceArgs.Devices["eth0"]
	if ok {
		data.Network = network["parent"]
	}

	for k, v := range m.instanceArgs.Devices {
		if v["type"] != "disk" || v["path"] == "/" {
			continue
		}

		data.Disks[k] = v
	}

	out, err := yaml.Marshal(&data)
	if err != nil {
		return ""
	}

	return string(out)
}

func (m *InstanceMigration) askProfiles() error {
	profileNames, err := m.server.GetProfileNames()
	if err != nil {
		return err
	}

	profiles, err := m.asker.AskString("Which profiles do you want to apply to the instance? (space separated) [default=default, \"-\" for none]: ", "default", func(s string) error {
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
		m.instanceArgs.Profiles = strings.Split(profiles, " ")
	}

	return nil
}

func (m *InstanceMigration) askConfig() error {
	configs, err := m.asker.AskString("Please specify config keys and values (key=value ...): ", "", func(s string) error {
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
		m.instanceArgs.Config[key] = value
	}

	return nil
}

func (m *InstanceMigration) askStorage() error {
	storagePools, err := m.server.GetStoragePoolNames()
	if err != nil {
		return err
	}

	if len(storagePools) == 0 {
		return errors.New("No storage pools available")
	}

	storagePool, err := m.asker.AskChoice("Please provide the storage pool to use: ", storagePools, "")
	if err != nil {
		return err
	}

	m.instanceArgs.Devices["root"] = map[string]string{
		"type": "disk",
		"pool": storagePool,
		"path": "/",
	}

	changeStorageSize, err := m.asker.AskBool("Do you want to change the storage size? [default=no]: ", "no")
	if err != nil {
		return err
	}

	if changeStorageSize {
		size, err := m.asker.AskString("Please specify the storage size: ", "", func(s string) error {
			_, err := units.ParseByteSizeString(s)
			return err
		})
		if err != nil {
			return err
		}

		m.instanceArgs.Devices["root"]["size"] = size
	}

	return nil
}

func (m *InstanceMigration) askDiskStorage() error {
	diskNames := []string{}
	for _, vol := range m.volumes {
		diskNames = append(diskNames, vol.customVolumeArgs.Name)
	}

	if len(diskNames) == 0 {
		return errors.New("No additional disks available")
	}

	diskName, err := m.asker.AskChoice("Please provide the disk name: ", diskNames, "")
	if err != nil {
		return err
	}

	storagePools, err := m.server.GetStoragePoolNames()
	if err != nil {
		return err
	}

	if len(storagePools) == 0 {
		return errors.New("No storage pools available")
	}

	storagePool, err := m.asker.AskChoice("Please provide the storage pool to use: ", storagePools, "")
	if err != nil {
		return err
	}

	m.instanceArgs.Devices[diskName]["pool"] = storagePool
	for _, vol := range m.volumes {
		if vol.customVolumeArgs.Name == diskName {
			vol.pool = storagePool
			break
		}
	}

	return nil
}

func (m *InstanceMigration) askNetwork() error {
	networks, err := m.server.GetNetworkNames()
	if err != nil {
		return err
	}

	network, err := m.asker.AskChoice("Please specify the network to use for the instance: ", networks, "")
	if err != nil {
		return err
	}

	m.instanceArgs.Devices["eth0"] = map[string]string{
		"type":    "nic",
		"nictype": "bridged",
		"parent":  network,
		"name":    "eth0",
	}

	return nil
}

func (m *InstanceMigration) askDisk() error {
	volMigrator, ok := NewVolumeMigration(m.ctx, m.server, m.asker, m.flagRsyncArgs).(*VolumeMigration)
	if !ok {
		return errors.New("Migrator should be of type VolumeMigration")
	}

	volMigrator.project = m.project

	err := volMigrator.gatherInfo()
	if err != nil {
		return err
	}

	if m.migrationType == MigrationTypeContainer && volMigrator.migrationType == MigrationTypeVolumeBlock {
		return errors.New("Block disk is not supported by the container")
	}

	m.instanceArgs.Devices[volMigrator.customVolumeArgs.Name] = map[string]string{
		"type":   "disk",
		"pool":   volMigrator.pool,
		"source": volMigrator.customVolumeArgs.Name,
	}

	if volMigrator.migrationType == MigrationTypeVolumeFilesystem {
		mountPath, err := m.asker.AskString("Provide mount path for this disk: ", "", nil)
		if err != nil {
			return err
		}

		m.instanceArgs.Devices[volMigrator.customVolumeArgs.Name]["path"] = mountPath
	}

	m.volumes = append(m.volumes, volMigrator)

	return nil
}

func (m *InstanceMigration) askUEFISupport() error {
	if m.instanceArgs.Type == api.InstanceTypeVM {
		architectureName, _ := osarch.ArchitectureGetLocal()

		if slices.Contains([]string{"x86_64", "aarch64"}, architectureName) {
			hasUEFI, err := m.asker.AskBool("Does the VM support UEFI booting? [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			if hasUEFI {
				hasSecureBoot, err := m.asker.AskBool("Does the VM support UEFI Secure Boot? [default=yes]: ", "yes")
				if err != nil {
					return err
				}

				if !hasSecureBoot {
					m.instanceArgs.Config["security.secureboot"] = "false"
				}
			} else {
				m.instanceArgs.Config["security.csm"] = "true"
				m.instanceArgs.Config["security.secureboot"] = "false"
			}
		}
	}

	return nil
}
