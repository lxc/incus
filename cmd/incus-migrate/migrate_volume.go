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
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ask"
	"github.com/lxc/incus/v6/shared/revert"
)

// VolumeMigration handles the migration logic for an custom volume.
type VolumeMigration struct {
	*Migration

	customVolumeArgs api.StorageVolumesPost
	flagRsyncArgs    string
}

// NewVolumeMigration returns a new VolumeMigration.
func NewVolumeMigration(ctx context.Context, server incus.InstanceServer, asker ask.Asker, flagRsyncArgs string) Migrator {
	return &VolumeMigration{
		Migration: &Migration{
			asker:  asker,
			ctx:    ctx,
			server: server,
		},
		flagRsyncArgs: flagRsyncArgs,
	}
}

// gatherInfo collects information from the user about the custom volume to be created.
func (m *VolumeMigration) gatherInfo() error {
	var err error

	m.customVolumeArgs = api.StorageVolumesPost{
		Type: "custom",
		Source: api.StorageVolumeSource{
			Type: "migration",
			Mode: "push",
		},
	}

	// Project
	if m.project == "" {
		err = m.askProject("Project to create the volume in [default=default]: ")
		if err != nil {
			return err
		}
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

	// Pool
	pools, err := m.server.GetStoragePools()
	if err != nil {
		return err
	}

	poolNames := []string{}
	for _, p := range pools {
		poolNames = append(poolNames, p.Name)
	}

	for {
		poolName, err := m.asker.AskString("Name of the pool: ", "", nil)
		if err != nil {
			return err
		}

		if !slices.Contains(poolNames, poolName) {
			fmt.Printf("Pool %q doesn't exists\n", poolName)
			continue
		}

		m.pool = poolName
		break
	}

	// Custom volume name
	volumes, err := m.server.GetStoragePoolVolumes(m.pool)
	if err != nil {
		return err
	}

	volumeNames := []string{}
	for _, v := range volumes {
		if v.Type != "custom" {
			continue
		}

		volumeNames = append(volumeNames, v.Name)
	}

	for {
		volumeName, err := m.asker.AskString("Name of the new custom volume: ", "", nil)
		if err != nil {
			return err
		}

		if slices.Contains(volumeNames, volumeName) {
			fmt.Printf("Storage volume %q already exists\n", volumeName)
			continue
		}

		m.customVolumeArgs.Name = volumeName
		break
	}

	err = m.askSourcePath("Please provide the path to a disk or filesystem: ")
	if err != nil {
		return err
	}

	err = m.setMigrationType()
	if err != nil {
		return err
	}

	if m.migrationType == MigrationTypeVolumeFilesystem {
		m.customVolumeArgs.ContentType = "filesystem"
	} else {
		m.customVolumeArgs.ContentType = "block"
	}

	err = m.setSourceFormat()
	if err != nil {
		return err
	}

	return nil
}

// migrate performs the custom volume migration.
func (m *VolumeMigration) migrate() error {
	if m.migrationType != MigrationTypeVolumeBlock && m.migrationType != MigrationTypeVolumeFilesystem {
		return errors.New("Wrong migration type for migrate")
	}

	// User decided not to migrate.
	if m.customVolumeArgs.Name == "" {
		return nil
	}

	return m.runMigration(func(path string) error {
		reverter := revert.New()
		defer reverter.Fail()

		// Create the custom volume
		op, err := m.server.CreateStoragePoolVolumeFromMigration(m.pool, m.customVolumeArgs)
		if err != nil {
			return err
		}

		reverter.Add(func() {
			_ = m.server.DeleteStoragePoolVolume(m.pool, "custom", m.customVolumeArgs.Name)
		})

		progress := cli.ProgressRenderer{Format: "Transferring custom volume: %s"}
		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		err = transferRootfs(m.ctx, op, path, m.flagRsyncArgs, m.migrationType)
		if err != nil {
			return err
		}

		progress.Done(fmt.Sprintf("Custom volume %s successfully created", m.customVolumeArgs.Name))
		reverter.Success()

		return nil
	})
}

// renderObject renders the state of the custom volume to be created.
func (m *VolumeMigration) renderObject() error {
	fmt.Println("\nCustom volume to be created:")

	scanner := bufio.NewScanner(strings.NewReader(m.render()))
	for scanner.Scan() {
		fmt.Printf("  %s\n", scanner.Text())
	}

	shouldMigrate, err := m.asker.AskBool("Do you want to continue? [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	// Reset volume settings when user interrupts creation process
	if !shouldMigrate {
		m.customVolumeArgs = api.StorageVolumesPost{}
	}

	return nil
}

func (m *VolumeMigration) render() string {
	data := struct {
		Name         string `yaml:"Name"`
		Project      string `yaml:"Project"`
		Type         string `yaml:"Type"`
		Source       string `yaml:"Source"`
		SourceFormat string `yaml:"Source format,omitempty"`
	}{
		m.customVolumeArgs.Name,
		m.project,
		m.customVolumeArgs.ContentType,
		m.sourcePath,
		m.sourceFormat,
	}

	out, err := yaml.Marshal(&data)
	if err != nil {
		return ""
	}

	return string(out)
}

func (m *VolumeMigration) setMigrationType() error {
	if m.sourcePath == "" {
		return errors.New("Missing source path")
	}

	if internalUtil.IsDir(m.sourcePath) {
		m.migrationType = MigrationTypeVolumeFilesystem
	} else {
		m.migrationType = MigrationTypeVolumeBlock
	}

	return nil
}
